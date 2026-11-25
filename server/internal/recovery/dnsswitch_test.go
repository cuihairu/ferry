package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// fakeDNS records every upsert (name|type|value); failures can be injected.
type fakeDNS struct {
	upserts []string
	err     error
}

func (f *fakeDNS) Kind() string { return "fake" }
func (f *fakeDNS) Upsert(ctx context.Context, name, rtype, value string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, name+"|"+rtype+"|"+value)
	return nil
}
func (f *fakeDNS) Delete(ctx context.Context, name, rtype string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, "del:"+name+"|"+rtype)
	return nil
}

// setupDNSAction seeds one enabled DNS provider and one front record, returning the action with the fake injected.
func setupDNSAction(t *testing.T, backups string, fake *fakeDNS) *DNSAction {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("dns-master")
	sealed, err := store.Encrypt("tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.0.0.1", BackupIPs: backups}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	// The blocked entry node (OnDone pipeline tests check that it got reset).
	node := storage.Node{Name: "entry-1", Port: 443, Protocol: "vless", Enabled: true,
		Token: "t-entry-1", Status: "online", PoolState: "suspended"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	return &DNSAction{
		DB: db, Store: store,
		New: func(kind, token string) (dns.Provider, error) {
			if kind != "cloudflare" || token != "tok-secret" {
				t.Fatalf("factory got kind=%q token=%q", kind, token)
			}
			return fake, nil
		},
	}
}

// loadFront fetches the single front record.
func loadFront(t *testing.T, a *DNSAction) storage.DNSFront {
	t.Helper()
	var f storage.DNSFront
	if err := a.DB.First(&f).Error; err != nil {
		t.Fatal(err)
	}
	return f
}

// TestDNSActionSwitch covers the L1 switch: backup IPs are taken by the rotation
// cursor, and switched is only recorded after the upsert succeeds; switching away again rotates to the next backup IP.
func TestDNSActionSwitch(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2","10.0.0.3"]`, fake)
	node := storage.Node{Name: "entry-1"}

	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	f := loadFront(t, a)
	if !f.Switched || f.CurrentIP != "10.0.0.2" || f.SwitchIndex != 1 {
		t.Fatalf("after switch = %+v", f)
	}
	if len(fake.upserts) != 1 || fake.upserts[0] != "edge.example.com|A|10.0.0.2" {
		t.Fatalf("upserts = %v", fake.upserts)
	}

	// Simulate another verdict round (manually reset the switched state): rotate to the next backup IP.
	if err := a.DB.Model(&storage.DNSFront{}).Where("id = ?", f.ID).
		Update("switched", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	f = loadFront(t, a)
	if f.CurrentIP != "10.0.0.3" || f.SwitchIndex != 2 {
		t.Fatalf("after rotate = %+v", f)
	}
}

// TestDNSActionRestore covers switching back: switched records are upserted back to PrimaryIP with switched cleared;
// without a configured front it fails with a reason (the orchestration advances to L2 on failure).
func TestDNSActionRestore(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	node := storage.Node{Name: "entry-1"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := loadFront(t, a)
	if f.Switched || f.CurrentIP != f.PrimaryIP {
		t.Fatalf("after restore = %+v", f)
	}
	if got := fake.upserts[len(fake.upserts)-1]; got != "edge.example.com|A|10.0.0.1" {
		t.Fatalf("last upsert = %q", got)
	}

	// Restoring with no switched records is a no-op.
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestDNSActionNotConfigured: no domain front configured - fails with a reason, never fakes success.
func TestDNSActionNotConfigured(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.DB.Delete(&storage.DNSFront{}, 1).Error; err != nil {
		t.Fatal(err)
	}
	err := a.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil {
		t.Fatal("unconfigured L1 must fail")
	}
	if len(fake.upserts) != 0 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestDNSActionUpsertFailed: the DNS provider errors - Run fails and advances (no switched half state).
func TestDNSActionUpsertFailed(t *testing.T) {
	fake := &fakeDNS{err: errors.New("api down")}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err == nil {
		t.Fatal("upsert failure must propagate")
	}
	f := loadFront(t, a)
	if f.Switched {
		t.Fatalf("half state leaked: %+v", f)
	}
}

// TestOnDoneRestoresDNS covers the recovery-finish hook: probe recovery (done) switches the
// front records back to normal via OnDone - closing the "reset -> switch back" loop.
func TestOnDoneRestoresDNS(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	node := storage.Node{Name: "entry-1"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}

	// Open a running recovery pipeline; the node is already reset (pool active).
	db := a.DB
	var n storage.Node
	if err := db.Where("name = ?", "entry-1").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
		Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 1, State: StateRunning,
		Action: "dns_switch", ActionState: ActionRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}

	var hookCalls int
	opts := Options{OnDone: func(nodeID uint, nodeName string) {
		hookCalls++
		if err := a.Restore(context.Background()); err != nil {
			t.Errorf("restore: %v", err)
		}
	}}
	if err := Sweep(db, now, opts, Registry{1: a}, nil); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 1 {
		t.Fatalf("hookCalls = %d", hookCalls)
	}
	f := loadFront(t, a)
	if f.Switched || f.CurrentIP != f.PrimaryIP {
		t.Fatalf("not restored: %+v", f)
	}
	var got storage.Recovery
	if err := db.First(&got, rec.ID).Error; err != nil || got.State != StateDone {
		t.Fatalf("recovery = %+v err=%v", got, err)
	}
}

// TestParseBackups covers the backup IP parsing rules: empty string / all-empty entries yield an empty set,
// and a non-string array yields a JSON error (the message names the config field for easier debugging).
func TestParseBackups(t *testing.T) {
	ips, err := parseBackups("")
	if err != nil || len(ips) != 0 {
		t.Fatalf("empty raw = %v err=%v", ips, err)
	}
	ips, err = parseBackups(`["",""]`)
	if err != nil || len(ips) != 0 {
		t.Fatalf("all blanks = %v err=%v", ips, err)
	}
	ips, err = parseBackups(`["10.0.0.2",""]`)
	if err != nil || len(ips) != 1 || ips[0] != "10.0.0.2" {
		t.Fatalf("filtered = %v err=%v", ips, err)
	}
	if _, err := parseBackups("not json"); err == nil ||
		!strings.Contains(err.Error(), "JSON string array") {
		t.Fatalf("bad json err = %v", err)
	}
	if _, err := parseBackups(`[1,2]`); err == nil ||
		!strings.Contains(err.Error(), "JSON string array") {
		t.Fatalf("number array err = %v", err)
	}
}

// TestDNSActionFactoryDefault: without an injected factory, defaults to the dns.New plugin slot.
func TestDNSActionFactoryDefault(t *testing.T) {
	if a := (&DNSAction{}); a.factory() == nil {
		t.Fatal("default factory must resolve to dns.New")
	}
}

// TestDNSActionNoMasterKey: master key not configured - Restore fails at decryption,
// never faking a successful switch-back.
func TestDNSActionNoMasterKey(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	// A second instance with only a DB and no secret store: restoring the switched record fails at providerFor.
	b := &DNSAction{DB: a.DB}
	err := b.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "master key not configured") {
		t.Fatalf("err = %v; want master key not configured", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("front must stay switched on failed restore: %+v", f)
	}
}

// TestDNSActionRestoreUpsertFailed: the DNS provider errors on switch-back - the
// failure surfaces and the switched state is not cleared (retried on the next finish round).
func TestDNSActionRestoreUpsertFailed(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	fake.err = errors.New("api down")
	err := a.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recovery: dns restore edge.example.com: api down") {
		t.Fatalf("err = %v; want restore upsert failure", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("front cleared on failed restore: %+v", f)
	}
}

// failUpdates registers an always-failing update callback (gorm error-injection slot) to simulate a write failure.
func failUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Callback().Update().Before("gorm:update").Register("test:fail_update",
		func(tx *gorm.DB) { tx.AddError(errors.New("db down")) }); err != nil {
		t.Fatal(err)
	}
}

// TestDNSActionSwitchDBUpdateFailed: the upsert succeeds but recording switched
// fails - the error surfaces and no half state remains (switched unset, re-switched next round).
func TestDNSActionSwitchDBUpdateFailed(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	failUpdates(t, a.DB)
	err := a.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v; want db failure", err)
	}
	if f := loadFront(t, a); f.Switched || f.CurrentIP != "" {
		t.Fatalf("half state leaked: %+v", f)
	}
}

// TestDNSActionRestoreDBUpdateFailed: the switch-back upsert succeeds but clearing
// switched fails to persist - the error surfaces and the record stays switched (safe for idempotent retry).
func TestDNSActionRestoreDBUpdateFailed(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	failUpdates(t, a.DB)
	err := a.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v; want db failure", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("switched cleared despite db failure: %+v", f)
	}
}
