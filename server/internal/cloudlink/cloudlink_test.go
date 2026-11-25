package cloudlink

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/oper"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// fakeSecrets is the in-memory secretStore for tests.
type fakeSecrets struct{}

func (fakeSecrets) Decrypt(string) (string, error) { return "sk-test", nil }

func linkTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "link.db"))
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

func linkTestAccount(t *testing.T, db *gorm.DB, fake *cloudprov.Fake) (storage.Provider, storage.CloudResource, storage.Node) {
	t.Helper()
	prov := storage.Provider{Name: "link", Type: "fake-link", AccessKey: "sealed", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatalf("provider: %v", err)
	}
	node := storage.Node{
		Name: "vm-1", Token: "tok-link", Port: 443, Protocol: "vless",
		Enabled: true, Status: "online", PoolState: pool.StateActive,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	res := storage.CloudResource{
		ProviderID: prov.ID, ExternalID: "vm-ext", Region: "fra1",
		State: cloudprov.StateRunning, NodeID: &node.ID, LastSeenAt: time.Now(),
	}
	if err := db.Create(&res).Error; err != nil {
		t.Fatalf("resource: %v", err)
	}
	return prov, res, node
}

func TestMapAndApply(t *testing.T) {
	db := linkTestDB(t)
	_, res, node := linkTestAccount(t, db, cloudprov.NewFake())

	// A stopped instance takes the Node out of scheduling but leaves the row
	// and the binding (F-0392); applying is idempotent (F-0398).
	eff := Map(cloudprov.StateStopped)
	if eff.Maintenance == nil || !*eff.Maintenance || eff.PoolState != pool.StateSuspended {
		t.Fatalf("stopped effect = %+v", eff)
	}
	changed, err := Apply(db, &node, eff, time.Now())
	if err != nil || !changed {
		t.Fatalf("apply: changed=%v err=%v", changed, err)
	}
	if changed, err := Apply(db, &node, eff, time.Now()); err != nil || changed {
		t.Fatalf("second apply should be a no-op: changed=%v err=%v", changed, err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateSuspended || !after.Enabled {
		t.Fatalf("node after stop = %+v", after)
	}
	// A running vendor state alone does not restore the Node (F-0397): Map
	// returns an empty effect and availability stays as-is.
	if eff := Map(cloudprov.StateRunning); eff.Maintenance != nil || eff.PoolState != "" {
		t.Fatalf("running effect must not claim availability: %+v", eff)
	}
	// A rebuild marks the Node for re-registration (F-0395).
	eff = Map(cloudprov.StateRebuilding)
	if eff.Note == nil || *eff.Note == "" {
		t.Fatalf("rebuild note missing: %+v", eff)
	}
	// A missing resource disables the Node but keeps the row (F-0396).
	eff = MapMissing()
	if eff.Enabled == nil || *eff.Enabled || eff.PoolState != pool.StateSuspended {
		t.Fatalf("missing effect = %+v", eff)
	}
	// The resource row is untouched by the mapping rules.
	var count int64
	db.Model(&storage.CloudResource{}).Where("id = ?", res.ID).Count(&count)
	if count != 1 {
		t.Fatalf("resource rows = %d, want 1", count)
	}
}

func TestRefreshAndApply(t *testing.T) {
	db := linkTestDB(t)
	fake := cloudprov.NewFake()
	prov, res, node := linkTestAccount(t, db, fake)
	fake.Seed(cloudprov.Resource{ResourceID: "vm-ext", State: cloudprov.StateStopped})
	svc := New(db, fakeSecrets{}, func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
		return fake, nil
	})

	// The vendor query drives the Node state (F-0398); a failure is returned
	// without touching the Node.
	eff, changed, err := svc.RefreshAndApply(context.Background(), prov.ID, res.ID, time.Now())
	if err != nil || !changed {
		t.Fatalf("refresh: eff=%+v changed=%v err=%v", eff, changed, err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateSuspended {
		t.Fatalf("node after stopped refresh = %+v", after)
	}
	// Unbound resources report ErrNoNode but still map the effect (F-0391).
	if err := db.Model(&res).Update("node_id", nil).Error; err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if _, _, err := svc.RefreshAndApply(context.Background(), prov.ID, res.ID, time.Now()); err != ErrNoNode {
		t.Fatalf("unbound err = %v, want ErrNoNode", err)
	}
}

func TestRefreshMissingResource(t *testing.T) {
	db := linkTestDB(t)
	fake := cloudprov.NewFake()
	prov, res, node := linkTestAccount(t, db, fake)
	fake.Seed(cloudprov.Resource{ResourceID: "vm-ext", State: cloudprov.StateRunning})
	svc := New(db, fakeSecrets{}, func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
		return fake, nil
	})
	fake.Unseed("vm-ext")
	_, _, err := svc.RefreshAndApply(context.Background(), prov.ID, res.ID, time.Now())
	if err != nil {
		t.Fatalf("refresh missing: %v", err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Enabled || !after.Maintenance || after.PoolState != pool.StateSuspended {
		t.Fatalf("node after missing = %+v", after)
	}
	var missing int64
	db.Model(&storage.CloudResource{}).Where("id = ? AND missing = ?", res.ID, true).Count(&missing)
	if missing != 1 {
		t.Fatalf("resource not marked missing")
	}
}

func TestApplyOperation(t *testing.T) {
	db := linkTestDB(t)
	fake := cloudprov.NewFake()
	prov, res, node := linkTestAccount(t, db, fake)
	fake.Seed(cloudprov.Resource{ResourceID: "vm-ext", State: cloudprov.StateStopped})
	svc := New(db, fakeSecrets{}, func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
		return fake, nil
	})
	op := storage.CloudOperation{
		ResourceID: res.ID, ProviderID: prov.ID, Op: cloudprov.OpPowerOn,
		State: oper.StateSucceeded, IdempotencyKey: "k-link-1",
	}
	if err := db.Create(&op).Error; err != nil {
		t.Fatalf("op: %v", err)
	}
	// A succeeded op is resolved by the vendor's real state: the instance is
	// stopped, so the Node stays out of scheduling (F-0397/F-0398).
	eff, changed, err := svc.ApplyOperation(context.Background(), op.ID, time.Now())
	if err != nil || !changed {
		t.Fatalf("apply op: eff=%+v changed=%v err=%v", eff, changed, err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateSuspended {
		t.Fatalf("node after stopped success = %+v", after)
	}
	// A failed op conservatively suspends without trusting the vendor call
	// (F-0372/F-0392).
	node2 := storage.Node{
		Name: "vm-2", Token: "tok-link-2", Port: 443, Protocol: "vless",
		Enabled: true, Status: "online", PoolState: pool.StateActive,
	}
	if err := db.Create(&node2).Error; err != nil {
		t.Fatalf("node2: %v", err)
	}
	res2 := storage.CloudResource{
		ProviderID: prov.ID, ExternalID: "vm-ext-2", State: cloudprov.StateRunning,
		NodeID: &node2.ID, LastSeenAt: time.Now(),
	}
	if err := db.Create(&res2).Error; err != nil {
		t.Fatalf("res2: %v", err)
	}
	op2 := storage.CloudOperation{
		ResourceID: res2.ID, ProviderID: prov.ID, Op: cloudprov.OpReboot,
		State: oper.StateFailed, IdempotencyKey: "k-link-2",
	}
	if err := db.Create(&op2).Error; err != nil {
		t.Fatalf("op2: %v", err)
	}
	if _, _, err := svc.ApplyOperation(context.Background(), op2.ID, time.Now()); err != nil {
		t.Fatalf("apply failed op: %v", err)
	}
	var n2 storage.Node
	if err := db.First(&n2, node2.ID).Error; err != nil {
		t.Fatalf("reload n2: %v", err)
	}
	if !n2.Maintenance || n2.PoolState != pool.StateSuspended {
		t.Fatalf("node after failed op = %+v", n2)
	}
}

func TestCheckNode(t *testing.T) {
	db := linkTestDB(t)
	fake := cloudprov.NewFake()
	_, _, node := linkTestAccount(t, db, fake)
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()

	// A fresh heartbeat plus a validated relay config is healthy (F-0393).
	recent := now.Add(-30 * time.Second)
	node.LastSeen = &recent
	node.Status = "online"
	if err := db.Save(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := db.Create(&storage.NodeConfig{
		NodeID: node.ID, Proc: "relay", Status: "applied", Validated: true,
	}).Error; err != nil {
		t.Fatalf("config: %v", err)
	}
	h, err := svc.CheckNode(node.ID, now)
	if err != nil || !h.Healthy {
		t.Fatalf("healthy check = %+v err=%v", h, err)
	}
	// A stale heartbeat fails the check even though the vendor is running
	// (F-0397).
	stale := now.Add(-10 * time.Minute)
	if err := db.Model(&node).Update("last_seen", stale).Error; err != nil {
		t.Fatalf("stale: %v", err)
	}
	h, err = svc.CheckNode(node.ID, now)
	if err != nil || h.Healthy {
		t.Fatalf("stale check should fail: %+v err=%v", h, err)
	}
	// An active crash alert fails the check too (F-0403).
	fresh := now.Add(-time.Second)
	if err := db.Model(&node).Update("last_seen", fresh).Error; err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if err := db.Create(&storage.Alert{
		NodeID: node.ID, Kind: "proc_crash", Severity: "critical",
		Message: "boom", State: "active",
	}).Error; err != nil {
		t.Fatalf("alert: %v", err)
	}
	h, err = svc.CheckNode(node.ID, now)
	if err != nil || h.Healthy {
		t.Fatalf("crash alert check should fail: %+v err=%v", h, err)
	}
}

func TestRestoreAfterBoot(t *testing.T) {
	db := linkTestDB(t)
	_, _, node := linkTestAccount(t, db, cloudprov.NewFake())
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()
	// The Node is suspended and unverified after a boot (F-0392/F-0393).
	node.Maintenance = true
	node.PoolState = pool.StateSuspended
	if err := db.Save(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	// No agent checks: restore refuses and the Node stays suspended.
	h, err := svc.Restore(node.ID, now)
	if err != nil || h.Healthy {
		t.Fatalf("restore without agent = %+v err=%v", h, err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateSuspended {
		t.Fatalf("node should stay suspended: %+v", after)
	}
	// A fresh heartbeat and validated config restore scheduling (F-0402).
	recent := now.Add(-30 * time.Second)
	if err := db.Model(&node).Update("last_seen", recent).Error; err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := db.Create(&storage.NodeConfig{
		NodeID: node.ID, Proc: "relay", Status: "applied", Validated: true,
	}).Error; err != nil {
		t.Fatalf("config: %v", err)
	}
	h, err = svc.Restore(node.ID, now)
	if err != nil || !h.Healthy {
		t.Fatalf("restore with agent = %+v err=%v", h, err)
	}
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Maintenance || after.PoolState != pool.StateActive {
		t.Fatalf("node should be restored: %+v", after)
	}
	// Every restore decision is in the audit trail (F-0404).
	var audits int64
	db.Model(&storage.AuditLog{}).Where("action = ?", "cloud_node_restore").Count(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
}

func TestAfterBootAlert(t *testing.T) {
	db := linkTestDB(t)
	_, _, node := linkTestAccount(t, db, cloudprov.NewFake())
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()
	// A stale heartbeat makes the boot fail and emits one alert (F-0403).
	stale := now.Add(-10 * time.Minute)
	if err := db.Model(&node).Update("last_seen", stale).Error; err != nil {
		t.Fatalf("stale: %v", err)
	}
	if _, err := svc.AfterBoot(node.ID, now); err != nil {
		t.Fatalf("after boot: %v", err)
	}
	var events []storage.Event
	if err := db.Where("kind = ?", herald.KindCloudBootFailed).Find(&events).Error; err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("boot-failed events = %d, want 1", len(events))
	}
	// A healthy boot emits no alert (F-0402).
	fresh := now.Add(-time.Second)
	if err := db.Model(&node).Updates(map[string]any{
		"last_seen": fresh, "status": "online", "maintenance": false,
		"pool_state": pool.StateActive,
	}).Error; err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if err := db.Create(&storage.NodeConfig{
		NodeID: node.ID, Proc: "relay", Status: "applied", Validated: true,
	}).Error; err != nil {
		t.Fatalf("config: %v", err)
	}
	before := len(events)
	if _, err := svc.AfterBoot(node.ID, now); err != nil {
		t.Fatalf("after boot healthy: %v", err)
	}
	if err := db.Where("kind = ?", herald.KindCloudBootFailed).Find(&events).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if len(events) != before {
		t.Fatalf("healthy boot emitted an alert: %d", len(events))
	}
}

func TestCheckShutdownRisk(t *testing.T) {
	db := linkTestDB(t)
	_, _, node := linkTestAccount(t, db, cloudprov.NewFake())
	svc := New(db, fakeSecrets{}, nil)
	now := time.Now()
	// No recent traffic: a shutdown is safe (F-0400).
	out, err := svc.CheckShutdownRisk(node.ID, now, 0)
	if err != nil || out.Risk {
		t.Fatalf("no traffic risk = %+v err=%v", out, err)
	}
	// Recent user traffic must be drained before shutdown (F-0400/F-0401).
	if err := db.Create(&storage.NodeTrafficLog{
		NodeID: node.ID, Proc: "relay", RxBytes: 2048, TxBytes: 4096,
		RecordedAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("traffic: %v", err)
	}
	out, err = svc.CheckShutdownRisk(node.ID, now, 0)
	if err != nil || !out.Risk || out.RecentConns == 0 {
		t.Fatalf("traffic risk = %+v err=%v", out, err)
	}
	// The drain takes the Node out of scheduling but keeps it online (F-0401).
	if err := svc.Drain(node.ID, "maintenance window", now); err != nil {
		t.Fatalf("drain: %v", err)
	}
	var after storage.Node
	if err := db.First(&after, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !after.Maintenance || after.PoolState != pool.StateStandby || after.Status != "online" {
		t.Fatalf("node after drain = %+v", after)
	}
}

func TestPathHealth(t *testing.T) {
	db := linkTestDB(t)
	fake := cloudprov.NewFake()
	_, _, node := linkTestAccount(t, db, fake)
	landing := storage.Node{
		Name: "land-1", Token: "tok-land", Port: 443, Protocol: "vless",
		Role: "landing", Enabled: true, Status: "online", PoolState: pool.StateActive,
	}
	if err := db.Create(&landing).Error; err != nil {
		t.Fatalf("landing: %v", err)
	}
	path := storage.Path{
		Name: "p-link", EntryNodeID: node.ID, LandingNodeID: landing.ID,
		Enabled: true, Transport: "tls",
	}
	if err := db.Create(&path).Error; err != nil {
		t.Fatalf("path: %v", err)
	}
	svc := New(db, fakeSecrets{}, nil)
	states, err := svc.PathHealth(node.ID, time.Now())
	if err != nil {
		t.Fatalf("path health: %v", err)
	}
	if states[path.ID] != "unknown" {
		t.Fatalf("active path health = %q, want unknown (no probe verdict)", states[path.ID])
	}
	// Taking the Node out of the pool degrades its paths (F-0399).
	node.Maintenance = true
	node.PoolState = pool.StateSuspended
	if err := db.Save(&node).Error; err != nil {
		t.Fatalf("suspend: %v", err)
	}
	states, err = svc.PathHealth(node.ID, time.Now())
	if err != nil {
		t.Fatalf("path health 2: %v", err)
	}
	if states[path.ID] == "healthy" {
		t.Fatalf("suspended node path health = %q", states[path.ID])
	}
}
