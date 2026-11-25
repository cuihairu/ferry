package provision

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestReplenishLaunchCtxBound covers the background launch bound: a tofu apply that hangs
// until its context dies still lands the job in a terminal (failed) state within the
// LaunchTimeout window, instead of running unbounded in the background.
func TestReplenishLaunchCtxBound(t *testing.T) {
	db := newTestDB(t)
	store := secret.NewStore("replenish-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "refill", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}

	saved := LaunchTimeout
	LaunchTimeout = 80 * time.Millisecond
	defer func() { LaunchTimeout = saved }()

	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, _ string, args []string, _ map[string]string) (string, error) {
		if len(args) > 0 && args[0] == "apply" {
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "inited", nil
	})
	r := &Replenisher{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(name string) LaunchParams {
			return LaunchParams{InstanceName: name, PanelWSURL: "ws://p",
				NewToken: func() (string, error) { return "tok-" + name, nil }}
		},
	}
	fired, err := r.Sweep(context.Background(), log.Default())
	if err != nil || !fired {
		t.Fatalf("empty pool fired=%v err=%v", fired, err)
	}

	var job storage.ProvisionJob
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := db.First(&job).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status == "failed" && job.FinishedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("launch never terminated: %+v", job)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
