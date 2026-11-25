package oper

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func operTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func operTestAccount(t *testing.T, db *gorm.DB, fake *cloudprov.Fake) (storage.Provider, storage.CloudResource) {
	t.Helper()
	sealed, err := secret.NewStore("test-master-key").Encrypt("sk-ops")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	p := storage.Provider{Name: "ops-provider", Type: "fake-ops", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	res := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-ops", Name: "one", Region: "fra1",
		State: cloudprov.StateStopped}
	if err := db.Create(&res).Error; err != nil {
		t.Fatalf("create resource: %v", err)
	}
	return p, res
}

func fakeAccountService(db *gorm.DB, fake *cloudprov.Fake) *Service {
	cloudprov.Register("fake-ops", func(cloudprov.Account) (cloudprov.Adapter, error) { return fake, nil })
	return New(db, secret.NewStore("test-master-key"))
}

// TestOperStateMachine covers F-0362/F-0368/F-0370: the six-state machine,
// state pre-checks and the high-risk second confirmation.
func TestOperStateMachine(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-ops", Name: "one", Region: "fra1", State: cloudprov.StateStopped})
	p, res := operTestAccount(t, db, f)
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")

	// Boot op on a stopped resource is accepted and settles succeeded.
	_, rows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}},
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rows[0].State != StatePending || rows[0].IdempotencyKey == "" {
		t.Fatalf("created row: %+v", rows[0])
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("run: %v", err)
	}
	row, logs, err := s.Get(context.Background(), rows[0].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != StateSucceeded || row.Result != "starting" {
		t.Fatalf("settled row: %+v", row)
	}
	if len(logs) < 3 {
		t.Fatalf("step log: %+v", logs)
	}
	// State refresh on the resource row is not automatic here (that is the
	// F-0398 controlled refresh), only the task result is stamped.

	// Reboot a running resource succeeds; a second reboot while already
	// transition-in is rejected (state pre-check).
	running := storage.CloudResource{State: cloudprov.StateRunning}
	if err := db.Model(&res).Updates(map[string]any{"state": cloudprov.StateRunning}).Error; err != nil {
		t.Fatalf("set running: %v", err)
	}
	_, rebootRows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpReboot, Specs: []OpSpec{{ResourceID: res.ID}}, IdempotencyKey: "k-reboot",
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("reboot create: %v", err)
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("reboot run: %v", err)
	}
	if rr, _, _ := s.Get(context.Background(), rebootRows[0].ID); rr.State != StateSucceeded {
		t.Fatalf("reboot settle: %+v", rr)
	}
	_ = running

	// Power-on on an already-running resource is refused by the pre-check.
	_, _, err = s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: res.ID}}, IdempotencyKey: "k-on-running",
	}, "tester", "admin", time.Now())
	if err == nil || !strings.Contains(err.Error(), "not in a state") {
		t.Fatalf("state pre-check: %v", err)
	}

	// High-risk op without confirmation is a 409-style sentinel (F-0370).
	_, _, err = s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOff, Specs: []OpSpec{{ResourceID: res.ID}},
		IdempotencyKey: "k-unconfirmed-off",
	}, "tester", "admin", time.Now())
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("confirmation sentinel: %v", err)
	}
	// Power-off is high-risk and needs the confirmation; with it, succeeds.
	off, offRows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOff, Specs: []OpSpec{{ResourceID: res.ID}},
		Confirmed: true, IdempotencyKey: "k-poweroff",
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("confirmed power-off: %v", err)
	}
	if !offRows[0].Confirmed || !offRows[0].AllowCancel {
		t.Fatalf("high-risk row: %+v", offRows[0])
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("power-off run: %v", err)
	}
	if r, _, _ := s.Get(context.Background(), off); r.State != StateSucceeded {
		t.Fatalf("power-off settle: %+v", r)
	}
}

// TestOperIdempotencyAndBatch covers F-0371/F-0377~F-0380: repeated
// submissions reuse the same rows and batches record per-resource results.
func TestOperIdempotencyAndBatch(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", State: cloudprov.StateRunning})
	f.Seed(cloudprov.Resource{ResourceID: "vm-2", State: cloudprov.StateRunning})
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-ops")
	p := storage.Provider{Name: "p", Type: "fake-ops", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	r1 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-1", State: cloudprov.StateRunning}
	r2 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-2", State: cloudprov.StateRunning}
	if err := db.Create(&r1).Error; err != nil || db.Create(&r2).Error != nil {
		t.Fatalf("resources: %v", err)
	}
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")

	// Explicit idempotency key replays the same rows (F-0371).
	req := Request{ProviderID: p.ID, Op: cloudprov.OpReboot,
		Specs: []OpSpec{{ResourceID: r1.ID}}, IdempotencyKey: "key-1"}
	firstID, firstRows, err := s.Create(context.Background(), req, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	secondID, secondRows, err := s.Create(context.Background(), req, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if firstID != secondID || len(firstRows) != len(secondRows) || firstRows[0].ID != secondRows[0].ID {
		t.Fatalf("idempotent replay: first=%d/%d second=%d/%d", firstID, len(firstRows), secondID, len(secondRows))
	}

	// Batch: two resources, one batch id, per-resource rows (F-0377/F-0380).
	_, batchRows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpReboot,
		Specs: []OpSpec{{ResourceID: r1.ID}, {ResourceID: r2.ID}},
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(batchRows) != 2 || batchRows[0].BatchID == "" || batchRows[0].BatchID != batchRows[1].BatchID {
		t.Fatalf("batch rows: %+v", batchRows)
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("batch run: %v", err)
	}
	batchIDStr := batchRows[0].BatchID
	rows, err := s.List(context.Background(), batchIDStr, 0, "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("list batch: %d err=%v", len(rows), err)
	}
	for _, r := range rows {
		if r.State != StateSucceeded {
			t.Fatalf("batch row not succeeded: %+v", r)
		}
	}
	// Partial failure: vm-2 deleted from the vendor side -> the row fails and
	// vm-1 still succeeds (F-0383).
	f.Unseed("vm-2")
	r3 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-3", State: cloudprov.StateRunning}
	if err := db.Create(&r3).Error; err != nil {
		t.Fatalf("r3: %v", err)
	}
	_, partial, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpReboot,
		Specs: []OpSpec{{ResourceID: r1.ID}, {ResourceID: r3.ID}},
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("partial create: %v", err)
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("partial run: %v", err)
	}
	var r1Row, r3Row storage.CloudOperation
	db.First(&r1Row, partial[0].ID)
	db.First(&r3Row, partial[1].ID)
	if r1Row.State != StateSucceeded || r3Row.State != StateFailed || r3Row.Error == "" {
		t.Fatalf("partial results: r1=%+v r3=%+v", r1Row, r3Row)
	}
}

// TestOperUnknownAndCancel covers F-0372/F-0373/F-0374/F-0376: a vendor
// timeout lands Unknown, reconciliation settles it by query (no blind
// retry), failure reasons carry the provider code, and cancellability follows
// the capability declaration.
func TestOperUnknownAndCancel(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", State: cloudprov.StateStopped})
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-ops")
	p := storage.Provider{Name: "p", Type: "fake-ops", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	r1 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-1", State: cloudprov.StateStopped}
	if err := db.Create(&r1).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	cloudprov.Register("fake-ops", func(cloudprov.Account) (cloudprov.Adapter, error) { return f, nil })
	defer cloudprov.Unregister("fake-ops")
	s := New(db, secret.NewStore("test-master-key"))

	// Timeout: the adapter call fails with a deadline -> Unknown, not Failed.
	f.SetFailHook("power_on", func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrUnavailable, cloudprov.OpPowerOn, "vm-1", "", "")
	})
	_, rows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: r1.ID}},
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("run: %v", err)
	}
	row, _, err := s.Get(context.Background(), rows[0].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != StateUnknown || row.Error == "" {
		t.Fatalf("unknown row: %+v", row)
	}
	// Reconciliation settles by querying the vendor state; the timeout adapter
	// now reports success so the state query resolves to running (F-0373).
	f.SetFailHook("", nil)
	// The vendor actually applied the boot despite the timeout; the query
	// sees the new state and settles the task (F-0373).
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", State: cloudprov.StateRunning})
	if err := s.Reconcile(context.Background(), time.Now()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	row, _, err = s.Get(context.Background(), rows[0].ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if row.State != StateSucceeded {
		t.Fatalf("reconciled row: %+v", row)
	}
	// Cancelling a non-cancellable terminal row fails; a fresh pending row with
	// a cancellable op cancels cleanly (F-0374).
	if err := s.Cancel(context.Background(), rows[0].ID, time.Now()); err == nil {
		t.Fatalf("terminal cancel should fail")
	}
	_, pending, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: r1.ID}}, IdempotencyKey: "k-pending",
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if !pending[0].AllowCancel {
		t.Fatalf("power op should be cancellable")
	}
	if err := s.Cancel(context.Background(), pending[0].ID, time.Now()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	cancelled, _, _ := s.Get(context.Background(), pending[0].ID)
	if cancelled.State != StateCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled row: %+v", cancelled)
	}
	// Force stop is high-risk + requires confirmation + carries the vendor
	// code; the DB row must be running for the state pre-check to pass.
	if err := db.Model(&r1).Update("state", cloudprov.StateRunning).Error; err != nil {
		t.Fatalf("set running: %v", err)
	}
	f.SetFailHook("force_stop", func() *cloudprov.Error {
		return cloudprov.NewError(cloudprov.ErrUnsupported, cloudprov.OpForceStop, "vm-1", "E_STALL", "stuck")
	})
	if _, _, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpForceStop, Specs: []OpSpec{{ResourceID: r1.ID}},
	}, "tester", "admin", time.Now()); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("force stop confirmation: %v", err)
	}
	// The fake caps force stop, so the confirmed create passes the capability
	// gate; the forced failure then records the provider code.
	_, fsRows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpForceStop, Specs: []OpSpec{{ResourceID: r1.ID}},
		Confirmed: true, IdempotencyKey: "k-forcestop",
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("confirmed force stop: %v", err)
	}
	if err := s.Run(context.Background(), time.Now()); err != nil {
		t.Fatalf("force stop run: %v", err)
	}
	fs, _, _ := s.Get(context.Background(), fsRows[0].ID)
	if fs.State != StateFailed || fs.ProviderCode != "E_STALL" || !strings.Contains(fs.Error, "force_stop") {
		t.Fatalf("failure reason: %+v", fs)
	}
}

// TestOperCapabilityGate covers F-0363~F-0367: ops the adapter does not
// declare are refused at creation, before any vendor call.
func TestOperCapabilityGate(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	f.SetCaps(cloudprov.Capabilities{APIVersion: "2.1", Power: true, ForceStop: true, Rebuild: false, Billing: true})
	f.Seed(cloudprov.Resource{ResourceID: "vm-1", State: cloudprov.StateStopped})
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-ops")
	p := storage.Provider{Name: "p", Type: "fake-ops", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	r1 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-1", State: cloudprov.StateStopped}
	if err := db.Create(&r1).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")
	if _, _, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpRebuild, Specs: []OpSpec{{ResourceID: r1.ID}},
		Confirmed: true,
	}, "tester", "admin", time.Now()); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("rebuild capability: %v", err)
	}
	// Power ops still allowed.
	_, rows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpPowerOn, Specs: []OpSpec{{ResourceID: r1.ID}},
	}, "tester", "admin", time.Now())
	if err != nil || len(rows) != 1 {
		t.Fatalf("power create: %v %+v", err, rows)
	}
}

// TestOperParamsJSON covers F-0384: request parameters are snapshotted as JSON
// on the task row.
func TestOperParamsJSON(t *testing.T) {
	db := operTestDB(t)
	f := cloudprov.NewFake()
	sealed, _ := secret.NewStore("test-master-key").Encrypt("sk-ops")
	p := storage.Provider{Name: "p", Type: "fake-ops", AccessKey: sealed, Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	r1 := storage.CloudResource{ProviderID: p.ID, ExternalID: "vm-1", State: cloudprov.StateStopped}
	if err := db.Create(&r1).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	s := fakeAccountService(db, f)
	defer cloudprov.Unregister("fake-ops")
	_, rows, err := s.Create(context.Background(), Request{
		ProviderID: p.ID, Op: cloudprov.OpRebuild,
		Specs:     []OpSpec{{ResourceID: r1.ID, Params: map[string]any{"image": "ubuntu-24.04"}}},
		Confirmed: true,
	}, "tester", "admin", time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(rows[0].Params), &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params["image"] != "ubuntu-24.04" {
		t.Fatalf("params=%+v", params)
	}
}
