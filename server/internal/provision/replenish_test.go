package provision

import (
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// replenishFix builds a Replenisher with a template, credentials and an injected fake runner.
func replenishFix(t *testing.T, providerEnabled bool) (*Replenisher, *storage.ProvisionTemplate) {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("replenish-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: providerEnabled}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	if !providerEnabled {
		// GORM skips writing the zero value of a boolean that has a default, so disabling needs an explicit update.
		if err := db.Model(&prov).Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
	}
	tpl := storage.ProvisionTemplate{Name: "refill", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "applied", nil
	})
	r := &Replenisher{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(name string) LaunchParams {
			return LaunchParams{InstanceName: name, PanelWSURL: "ws://p", NewToken: func() (string, error) { return "tok-" + name, nil }}
		},
	}
	return r, &tpl
}

func countProvisioning(t *testing.T, r *Replenisher) int64 {
	t.Helper()
	var n int64
	r.Manager.DB().Model(&storage.Node{}).
		Where("name LIKE ?", "pool-%").Count(&n)
	return n
}

// TestReplenishSweep covers the empty-pool safety net: an empty pool with nothing in flight -> the machine is
// launched automatically (node row provisioning, job traced); it does not launch when the pool is not empty or something is in flight; and no template configured means no launch.
func TestReplenishSweep(t *testing.T) {
	r, _ := replenishFix(t, true)
	logger := log.Default()

	// No template configured: never triggers.
	r.TemplateID = 0
	if fired, err := r.Sweep(context.Background(), logger); err != nil || fired {
		t.Fatalf("disabled fired=%v err=%v", fired, err)
	}
	r.TemplateID = 999 // a nonexistent template is treated the same way: no launch
	if fired, err := r.Sweep(context.Background(), logger); err == nil || fired {
		t.Fatalf("missing template fired=%v err=%v", fired, err)
	}
	r.TemplateID = 1

	// The pool has an active entry: no launch.
	db := r.Manager.DB()
	if err := db.Create(&storage.Node{Name: "in-pool", Token: "t1", Role: "entry",
		Enabled: true, Status: "online", PoolState: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if fired, err := r.Sweep(context.Background(), logger); err != nil || fired {
		t.Fatalf("non-empty pool fired=%v err=%v", fired, err)
	}

	// The pool is empty: launch (the node row goes to provisioning, and the OS-4 pipeline takes over admission into the pool).
	if err := db.Where("name = ?", "in-pool").Delete(&storage.Node{}).Error; err != nil {
		t.Fatal(err)
	}
	fired, err := r.Sweep(context.Background(), logger)
	if err != nil || !fired {
		t.Fatalf("empty pool fired=%v err=%v", fired, err)
	}
	// Execution happens in a background goroutine: poll for the node row to appear (with a sleep; a tight
	// poll without sleeping would exhaust its attempts before the goroutine gets scheduled under CI load).
	deadline := 100
	for countProvisioning(t, r) == 0 && deadline > 0 {
		time.Sleep(20 * time.Millisecond)
		deadline--
	}
	if countProvisioning(t, r) == 0 {
		t.Fatal("no provisioning node created")
	}
	// Poll for the job to be written back with its terminal state (with a sleep, as above).
	var job storage.ProvisionJob
	ok := false
	for i := 0; i < 100; i++ {
		if err := db.First(&job).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status == "ok" {
			ok = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("job never finished: %+v", job)
	}

	// Re-entrancy guard: a provisioning entry exists (launched in the previous round) and the pool is empty -> no further launch.
	fired, err = r.Sweep(context.Background(), logger)
	if err != nil || fired {
		t.Fatalf("in-flight must block, fired=%v err=%v", fired, err)
	}
}

// TestReplenishProviderDisabled the template's provider is disabled: it errors and does not launch, without claiming a trigger.
func TestReplenishProviderDisabled(t *testing.T) {
	r, _ := replenishFix(t, false)
	_, err := r.Sweep(context.Background(), log.Default())
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err = %v", err)
	}
}

// TestReplenishLoop covers the safety-net loop: the first round sweeps immediately (interval<=0 falls back to 5m),
// a new machine is launched when the pool is empty, and it returns once ctx is cancelled.
func TestReplenishLoop(t *testing.T) {
	r, _ := replenishFix(t, true)
	r.TemplateID = 1 // the template created by replenishFix is 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ReplenishLoop(ctx, r, 0, log.Default())
	}()

	deadline := 100
	for countProvisioning(t, r) == 0 && deadline > 0 {
		time.Sleep(20 * time.Millisecond)
		deadline--
	}
	if countProvisioning(t, r) == 0 {
		t.Fatal("loop sweep did not launch a node")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("replenish loop did not stop after cancel")
	}
}
