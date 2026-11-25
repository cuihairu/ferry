package recovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAdvanceUnknownActionStateFallsThrough: an unrecognized action-state value neither
// advances nor starts anything, silently waiting for a human (defends against dirty data).
func TestAdvanceUnknownActionStateFallsThrough(t *testing.T) {
	db := newTestDB(t)
	changed := time.Now().Add(-DefaultSustainedAfter - time.Minute)
	n := suspendedNode(t, db, "weird-1", "auto", changed)
	if err := Sweep(db, time.Now(), Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// After opening, set the action state to a dirty value and sweep once more: the state machine does not move.
	rec := loadRecovery(t, db, n.ID)
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
		Update("action_state", "weird").Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, time.Now().Add(3*time.Hour), Options{}, Registry{}, nil); err != nil {
		t.Fatalf("sweep with unknown action_state: %v", err)
	}
	after := loadRecovery(t, db, n.ID)
	if after.ActionState != "weird" || after.Level != rec.Level {
		t.Fatalf("row = %+v; unknown action_state must not advance", after)
	}
}

// TestDNSActionSwitchableNoProviders: no DNS provider configured - switching reports not-configured, restoring is a no-op.
func TestDNSActionSwitchableNoProviders(t *testing.T) {
	a := &DNSAction{DB: newTestDB(t)}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "dns front not configured") {
		t.Fatalf("run err = %v", err)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatalf("restore with nothing switched = %v; want nil", err)
	}
}

// TestDNSActionBadBackupIPs: the backup IP list is not a JSON array - switching errors and keeps the domain in the message.
func TestDNSActionBadBackupIPs(t *testing.T) {
	a := setupDNSAction(t, "{oops", &fakeDNS{})
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "dns front edge.example.com") ||
		!strings.Contains(err.Error(), "backup_ips") {
		t.Fatalf("err = %v; want backup_ips parse error", err)
	}
}

// TestDNSActionNoBackupIPs: all backup IPs empty - switching reports no usable backups.
func TestDNSActionNoBackupIPs(t *testing.T) {
	a := setupDNSAction(t, `["", ""]`, &fakeDNS{})
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "no backup ips") {
		t.Fatalf("err = %v", err)
	}
}

// TestDNSActionNoMasterKeyOnSwitch: the credential entry is not configured - switching fails right at the decryption boundary.
func TestDNSActionNoMasterKeyOnSwitch(t *testing.T) {
	a := setupDNSAction(t, `["1.1.1.1"]`, &fakeDNS{})
	a.Store = nil
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("err = %v", err)
	}
}

// TestDNSActionDecryptFailure: the credential ciphertext cannot be decrypted (old ciphertext after a key rotation) - the error names the provider.
func TestDNSActionDecryptFailure(t *testing.T) {
	a := setupDNSAction(t, `["1.1.1.1"]`, &fakeDNS{})
	if err := a.DB.Model(&storage.DNSProvider{}).Where("1=1").
		Update("api_key", "v1:not-valid:cipher").Error; err != nil {
		t.Fatal(err)
	}
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "dns provider cf") {
		t.Fatalf("err = %v", err)
	}
}

// TestInstanceActionTemplateErrors covers the three missing template/provider tiers: the error is passed through with its reason.
func TestInstanceActionTemplateErrors(t *testing.T) {
	db := newTestDB(t)
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	launch := func(string) provision.LaunchParams {
		return provision.LaunchParams{NewToken: func() (string, error) { return "tok", nil }}
	}
	store := secret.NewStore("l3-master")

	// The template does not exist.
	a := &InstanceAction{Manager: m, Store: store, TemplateID: 99999, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "recovery template 99999") {
		t.Fatalf("missing template err = %v", err)
	}

	// The template points to a non-existent provider.
	tpl := storage.ProvisionTemplate{Name: "t1", ProviderID: 99999}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	a = &InstanceAction{Manager: m, Store: store, TemplateID: tpl.ID, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "template provider missing") {
		t.Fatalf("missing provider err = %v", err)
	}

	// The provider is disabled.
	store2 := secret.NewStore("l3-master")
	sealed, err := store2.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	// gorm skips writing a zero bool that carries a default; disabling needs an explicit update.
	if err := db.Model(&prov).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	tpl2 := storage.ProvisionTemplate{Name: "t2", ProviderID: prov.ID}
	if err := db.Create(&tpl2).Error; err != nil {
		t.Fatal(err)
	}
	a = &InstanceAction{Manager: m, Store: store2, TemplateID: tpl2.ID, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "template provider disabled") {
		t.Fatalf("disabled provider err = %v", err)
	}
}

// TestInstanceActionApplyFailed: run failed - a non-ok status errors with the tail log line.
func TestInstanceActionApplyFailed(t *testing.T) {
	db := newTestDB(t)
	store := secret.NewStore("l3-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "t3", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		if args[0] == "apply" {
			return "step1\nboom", context.DeadlineExceeded
		}
		return "inited", nil
	})
	a := &InstanceAction{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(string) provision.LaunchParams {
			return provision.LaunchParams{NewToken: func() (string, error) { return "tok", nil }}
		},
	}
	err = a.Run(context.Background(), storage.Node{Name: "entry-9"})
	if err == nil || !strings.Contains(err.Error(), "recovery apply failed: ") {
		t.Fatalf("err = %v; want last line of failed log", err)
	}
	// lastLine takes the execution log's tail line: errString appends the error text at the end.
	if !strings.HasSuffix(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v; want log tail line", err)
	}
}

// TestStandbyActionNotWired: the standby action without a DB - fails with a reason, never faking success.
func TestStandbyActionNotWired(t *testing.T) {
	a := &StandbyAction{}
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "standby action not wired") {
		t.Fatalf("err = %v", err)
	}
}
