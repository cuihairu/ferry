package geodns

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

// fakeChan records every upsert/delete (name|value and name), injectable for failures.
type fakeChan struct {
	upserts []string
	deletes []string
	err     error
}

func (f *fakeChan) Kind() string { return "fake" }
func (f *fakeChan) Upsert(_ context.Context, name, rtype, value string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, name+"|"+rtype+"|"+value)
	return nil
}
func (f *fakeChan) Delete(_ context.Context, name, rtype string) error {
	if f.err != nil {
		return f.err
	}
	f.deletes = append(f.deletes, name+"|"+rtype)
	return nil
}

// newTestDB builds an in-memory DB (storage.Open + AutoMigrate, same policy as the recovery tests).
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// newSyncer builds an in-memory-DB Syncer (with a fake channel injected): enabled DNS vendor + front domain.
func newSyncer(t *testing.T, fake *fakeChan) (*Syncer, *fakeChan) {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("geo-master")
	sealed, err := store.Encrypt("tok-geo")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.9.9.1", BackupIPs: `["10.9.9.2"]`}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	s := &Syncer{DB: db, Store: store, New: func(kind, token string) (dns.Provider, error) {
		if kind != "cloudflare" || token != "tok-geo" {
			t.Fatalf("factory got kind=%q token=%q", kind, token)
		}
		return fake, nil
	}}
	return s, fake
}

// addNode inserts one in-pool entry node; an empty region means the unknown region.
func addNode(t *testing.T, s *Syncer, name, region string, rttMs int) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Region: region, Port: 443, Protocol: "vless",
		Enabled: true, Token: "t-" + name, Status: "online",
		Role: "entry", PoolState: "active", Address: ipOf(name)}
	if err := s.DB.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	if rttMs > 0 {
		pr := storage.ProbeReport{NodeID: n.ID, TargetKind: "tunnel", Reachable: true,
			RttMs: rttMs, ProbedAt: time.Now()}
		if err := s.DB.Create(&pr).Error; err != nil {
			t.Fatal(err)
		}
	}
	return n
}

func ipOf(name string) string { return "10.1.0." + name[len(name)-1:] }

// records takes all rows of the synchronized face.
func records(t *testing.T, s *Syncer) []storage.GeoDNSRecord {
	t.Helper()
	rows := []storage.GeoDNSRecord{}
	if err := s.DB.Order("slug").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestSlug region-vocabulary normalization: case/underscore/whitespace/illegal chars; purely Chinese normalizes to empty.
func TestSlug(t *testing.T) {
	cases := map[string]string{
		"US-East":    "us-east",
		"  Asia JP ": "asia-jp",
		"hk_01":      "hk-01",
		"欧 洲":        "",
		"--de--":     "de",
		"SG@a b":     "sga-b", // illegal chars stripped without inserting separators, only the charset is guaranteed legal
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegionReps takes the first of each region in ascending RTT, no-data trailing; out-of-pool / non-entry nodes do not appear.
func TestRegionReps(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	slow := addNode(t, s, "us-slow", "US", 300)
	fast := addNode(t, s, "us-fast", "US", 80)
	nodata := addNode(t, s, "us-nodata", "US", 0)
	addNode(t, s, "jp-entry", "JP", 50)
	// Removed/disabled nodes do not appear.
	addNode(t, s, "hk-out", "HK", 10)
	if err := s.DB.Model(&storage.Node{}).Where("name = ?", "hk-out").
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	addNode(t, s, "sg-off", "SG", 10)
	if err := s.DB.Model(&storage.Node{}).Where("name = ?", "sg-off").
		UpdateColumn("enabled", false).Error; err != nil { // disabled nodes do not appear
		t.Fatal(err)
	}

	reps, err := s.RegionReps(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("regions = %d (%v), want 2", len(reps), reps)
	}
	if reps["US"].ID != fast.ID {
		t.Errorf("US rep = %s (want us-fast, nodata id=%d slow id=%d)", reps["US"].Name, nodata.ID, slow.ID)
	}
	if reps["JP"].Name != "jp-entry" {
		t.Errorf("JP rep = %s", reps["JP"].Name)
	}
}

// TestSyncNewThenIdempotent the first round lands new records, the second round is idempotent with zero changes.
func TestSyncNewThenIdempotent(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 200)

	n, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("changed = %d, want 2", n)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
	rows := records(t, s)
	if len(rows) != 2 || rows[0].Name != "jp.edge.example.com" || rows[0].Value != ipOf("jp-1") {
		t.Fatalf("records = %+v", rows)
	}

	// second round: no change, zero upserts.
	n, err = s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sync = %d, %v; want 0, nil", n, err)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts after 2nd = %v", fake.upserts)
	}
}

// TestSyncRttFlipRewrites when the representative flips on an RTT flip: Upsert the same row (ID unchanged).
func TestSyncRttFlipRewrites(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	a := addNode(t, s, "us-a", "US", 100)
	addNode(t, s, "us-b", "US", 200)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := records(t, s)
	if len(rows) != 1 || rows[0].Value != ipOf("us-a") {
		t.Fatalf("records = %+v", rows)
	}
	firstID := rows[0].ID

	// us-b gets fast: the representative changes, the original row is rewritten.
	if err := s.DB.Model(&storage.ProbeReport{}).Where("node_id = ?", a.ID).
		Update("rtt_ms", 500.0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows = records(t, s)
	if len(rows) != 1 || rows[0].Value != ipOf("us-b") || rows[0].ID != firstID {
		t.Fatalf("after flip records = %+v (first id=%d)", rows, firstID)
	}
	if len(fake.upserts) != 2 || fake.upserts[1] != "us.edge.example.com|A|"+ipOf("us-b") {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestSyncRegionGoneDeletes full-outage record withdrawal: channel Delete plus local row deletion.
func TestSyncRegionGoneDeletes(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	a := addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 50)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(records(t, s)) != 2 {
		t.Fatal("expected 2 records")
	}

	// the US region's only entry is removed: its slug record is withdrawn.
	if err := s.DB.Model(&storage.Node{}).Where("id = ?", a.ID).
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	n, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(fake.deletes) != 1 || fake.deletes[0] != "us.edge.example.com|A" {
		t.Fatalf("changed=%d deletes=%v", n, fake.deletes)
	}
	rows := records(t, s)
	if len(rows) != 1 || rows[0].Slug != "jp" {
		t.Fatalf("records = %+v", rows)
	}
}

// TestSyncChineseRegionSkipped a purely Chinese region name normalizes to empty and produces no record.
func TestSyncChineseRegionSkipped(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "cn-1", "华东", 100)
	n, err := s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("changed=%d err=%v; want 0 no-op", n, err)
	}
	if len(fake.upserts) != 0 || len(records(t, s)) != 0 {
		t.Fatal("chinese region must not produce records")
	}
}

// TestSyncNoProvider with no enabled DNS vendor configured: a no-op (the feature is not enabled).
func TestSyncNoProvider(t *testing.T) {
	fake := &fakeChan{}
	s, _ := newSyncer(t, fake)
	if err := s.DB.Where("1=1").Delete(&storage.DNSProvider{}).Error; err != nil {
		t.Fatal(err)
	}
	addNode(t, s, "us-1", "US", 100)
	n, err := s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("changed=%d err=%v; want 0 no-op", n, err)
	}
}

// TestSyncFailureAlerts on channel failure: the error surfaces and only one alert goes out with per-day dedup;
// the remaining records keep processing (partial failure does not interrupt everything).
func TestSyncFailureAlerts(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 50)
	fake.err = errors.New("api down")

	n, err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("channel failure must surface")
	}
	if n != 0 {
		t.Fatalf("changed = %d, want 0", n)
	}
	var alerts int64
	if err := s.DB.Model(&storage.Event{}).Where("kind = ?", "geodns_sync_failed").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("alerts = %d, want 1 (dedup per day)", alerts)
	}

	// retry and fail again on the same day: the alert is not duplicated.
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("must still fail")
	}
	if err := s.DB.Model(&storage.Event{}).Where("kind = ?", "geodns_sync_failed").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("alerts after retry = %d, want 1", alerts)
	}

	// succeeds and lands a record after recovery.
	fake.err = nil
	if n, err := s.Sync(context.Background()); err != nil || n != 2 {
		t.Fatalf("recovered sync = %d, %v", n, err)
	}
}

// TestFactoryDefault a default dns.New plugin slot when no factory is injected.
func TestFactoryDefault(t *testing.T) {
	if f := (&Syncer{}).factory(); f == nil {
		t.Fatal("default factory must resolve to dns.New")
	}
}

// TestSyncCredentialFailures credential-chain failure: an unconfigured master key / undecryptable
// credentials both error out rather than claiming reconciliation success.
func TestSyncCredentialFailures(t *testing.T) {
	fake := &fakeChan{}
	s, _ := newSyncer(t, fake)
	addNode(t, s, "us-1", "US", 100)

	// master key not configured.
	noStore := &Syncer{DB: s.DB, New: s.New}
	if _, err := noStore.Sync(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "master key not configured") {
		t.Fatalf("err = %v; want master key not configured", err)
	}
	// credential ciphertext corrupt.
	if err := s.DB.Model(&storage.DNSProvider{}).Where("id = ?", 1).
		Update("api_key", "not-sealed").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "dns provider") {
		t.Fatalf("err = %v; want provider failure", err)
	}
}

// TestEmitSyncFailedToleratesDBError the event table is unwritable: the alert helper only logs and does not crash.
func TestEmitSyncFailedToleratesDBError(t *testing.T) {
	db := newTestDB(t)
	if err := db.Migrator().DropTable(&storage.Event{}); err != nil {
		t.Fatal(err)
	}
	emitSyncFailed(db, errors.New("boom")) // the event cannot be persisted, but not panicking passes
}
