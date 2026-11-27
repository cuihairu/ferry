package oper

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCloudOpFailedAlert covers F-1829: an operation settling failed lands
// exactly one cloud_op_failed warning keyed on the op row; a succeeding run
// stays quiet; the unknown settlement does not alert (its reconciliation
// outcome will, if it lands failed).
func TestCloudOpFailedAlert(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-fail", Name: "one", Region: "fra1",
		State: cloudprov.StateStopped})
	p, res := operTestAccount(t, db, f)
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")
	ctx := context.Background()

	// A credentials rejection is non-retryable: the run settles failed.
	f.SetFailHook(cloudprov.OpPowerOn, func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrAuth, cloudprov.OpPowerOn, "vm-fail", "401", "denied")
	})
	_, pending, err := s.Create(ctx, Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Run(ctx, time.Now()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var op storage.CloudOperation
	if err := db.First(&op, pending[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if op.State != StateFailed {
		t.Fatalf("op state = %q, want failed", op.State)
	}
	var ev storage.Event
	if err := db.Where("kind = ?", herald.KindCloudOpFailed).First(&ev).Error; err != nil {
		t.Fatalf("cloud_op_failed event missing: %v", err)
	}
	if ev.DedupKey != fmt.Sprintf("cloudop:%d:failed", pending[0].ID) {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}

	// A clean boot must not alert.
	f.SetFailHook(cloudprov.OpPowerOn, nil)
	if _, _, err := s.Create(ctx, Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
	}, "tester", "admin", time.Now()); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if err := s.Run(ctx, time.Now()); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindCloudOpFailed).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("success must stay quiet: %d events", n)
	}
}
