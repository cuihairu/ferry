package provision

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestProvisionFailedAlert covers F-1829: a job whose tofu run fails lands
// exactly one provision_failed warning keyed on the job row; a clean apply
// stays quiet; the replenish path (FinishJob) alerts through the same helper.
func TestProvisionFailedAlert(t *testing.T) {
	db := newTestDB(t)
	tpl := storage.ProvisionTemplate{Name: "hk-bad", Plan: "vc2-1c-1gb", Region: "hkg", Image: "docker"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m := New(db, "tofu", root, func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "terraform failed", errors.New("exit 1")
	})

	job, err := m.Apply(context.Background(), execParams(tpl, "vultr", "sk-live-key", "ferry-hk-bad", "apply"))
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "failed" {
		t.Fatalf("job status = %q, want failed", job.Status)
	}
	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindProvisionFailed).First(&ev).Error; err != nil {
		t.Fatalf("provision_failed event missing: %v", err)
	}
	if ev.DedupKey != fmt.Sprintf("provision_job:%d:failed", job.ID) {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}

	// A clean apply stays quiet.
	m2 := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "applied", nil
	})
	if _, err := m2.Apply(context.Background(), execParams(tpl, "vultr", "sk-live-key", "ferry-hk-ok", "apply")); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindProvisionFailed).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("clean apply must stay quiet: %d events", n)
	}

	// The replenish path alerts through the same helper.
	rjob := storage.ProvisionJob{TemplateID: tpl.ID, TemplateName: tpl.Name,
		InstanceName: "ferry-hk-rel", Action: "apply", Status: "running"}
	if err := db.Create(&rjob).Error; err != nil {
		t.Fatal(err)
	}
	m.FinishJob(rjob.ID, "failed", "apply bombed out")
	var ev2 storage.Event
	if err := db.Where("kind = ? AND id > ?", herald.KindProvisionFailed, ev.ID).
		First(&ev2).Error; err != nil {
		t.Fatalf("FinishJob path must alert: %v", err)
	}
}
