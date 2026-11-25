package cloudsync

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func syncTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "sync.db"))
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

func syncTestProvider(t *testing.T, db *gorm.DB, region string) storage.Provider {
	t.Helper()
	sealed, err := secret.NewStore("test-master-key").Encrypt("sk-test")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	p := storage.Provider{Name: "test", Type: "fake-sync", AccessKey: sealed, Enabled: true, Region: region}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return p
}

// TestSyncOneReconcile covers F-0342~F-0352: account/region/external id
// recorded, tags stored, repeat sync updates instead of duplicating, and
// provider-side deletion marks the row missing without deleting bindings.
func TestSyncOneReconcile(t *testing.T) {
	db := syncTestDB(t)
	p := syncTestProvider(t, db, "")
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "r-1", Name: "one", Region: "fra1",
		State: cloudprov.StateRunning, PublicIP: "1.2.3.4", BwMbps: 100,
		Spec: "s-1", Tags: map[string]string{"ferry": "managed"}})
	f.Seed(cloudprov.Resource{ResourceID: "r-2", Name: "two", Region: "nyc1",
		State: cloudprov.StateStopped})
	store := secret.NewStore("test-master-key")
	cfg := SyncConfig{Adapter: f, Tags: map[string]string{"ferry": "managed"}}
	now := time.Now()

	r := SyncOne(context.Background(), db, store, p.ID, "manual", cfg, now)
	if !r.OK || r.Added != 1 || r.Updated != 0 || r.Missing != 0 {
		t.Fatalf("first sync: %+v", r)
	}
	var rows []storage.CloudResource
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ExternalID != "r-1" || rows[0].ProviderID != p.ID ||
		rows[0].Region != "fra1" || rows[0].Tags != `{"ferry":"managed"}` || rows[0].BwMbps != 100 {
		t.Fatalf("first sync row: %+v", rows)
	}
	if rows[0].LastSyncAt == nil || rows[0].LastSeenAt.IsZero() {
		t.Fatalf("sync stamps missing: %+v", rows[0])
	}
	nodeID := uint(77)
	if err := db.Model(&rows[0]).Update("node_id", nodeID).Error; err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Tag filter excluded r-2 on the first pass; include it now and drop r-1
	// from the vendor list entirely: r-1 must be marked missing, not deleted.
	cfg.Tags = nil
	f.Unseed("r-1")
	r = SyncOne(context.Background(), db, store, p.ID, "manual", cfg, now.Add(time.Second))
	if !r.OK || r.Added != 1 || r.Missing != 1 {
		t.Fatalf("second sync: %+v", r)
	}
	var missing storage.CloudResource
	if err := db.Where("external_id = ?", "r-1").First(&missing).Error; err != nil {
		t.Fatalf("provider deletion destroyed the row: %v", err)
	}
	if !missing.Missing || missing.MissingSince == nil || missing.NodeID == nil || *missing.NodeID != nodeID {
		t.Fatalf("missing marker/binding: %+v", missing)
	}
	var added storage.CloudResource
	if err := db.Where("external_id = ?", "r-2").First(&added).Error; err != nil || added.Missing {
		t.Fatalf("added row: %+v err=%v", added, err)
	}
	// Reappearing resource clears the missing marker (duplicate import dedup).
	f.Seed(cloudprov.Resource{ResourceID: "r-1", Name: "one", Region: "fra1", State: cloudprov.StateRunning})
	r = SyncOne(context.Background(), db, store, p.ID, "manual", cfg, now.Add(2*time.Second))
	if !r.OK || r.Updated != 2 || r.Missing != 0 {
		t.Fatalf("third sync: %+v", r)
	}
	// Re-query into a fresh struct: First into the same struct retains old
	// field values for NULL columns, which hides the cleared marker.
	var reappeared storage.CloudResource
	if err := db.Where("external_id = ?", "r-1").First(&reappeared).Error; err != nil || reappeared.Missing || reappeared.MissingSince != nil {
		t.Fatalf("reappearance: %+v err=%v", reappeared, err)
	}
	// Two vendor records with the same id collapse to one row.
	f.Seed(cloudprov.Resource{ResourceID: "r-1", Name: "one-dup", Region: "fra1"})
	r = SyncOne(context.Background(), db, store, p.ID, "manual", cfg, now.Add(3*time.Second))
	if !r.OK {
		t.Fatalf("dedup sync: %+v", r)
	}
	var count int64
	if err := db.Model(&storage.CloudResource{}).Where("external_id = ?", "r-1").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate import count=%d err=%v", count, err)
	}
}

// TestSyncScope covers F-0344/F-0348: a region-scoped run only reconciles
// rows in that region, and Provider.Region is an authorization boundary that
// refuses an out-of-scope request (F-0334).
func TestSyncScope(t *testing.T) {
	db := syncTestDB(t)
	p := syncTestProvider(t, db, "fra1")
	f := cloudprov.NewFake()
	f.Seed(cloudprov.Resource{ResourceID: "r-1", Name: "one", Region: "fra1"})
	f.Seed(cloudprov.Resource{ResourceID: "r-2", Name: "two", Region: "nyc1"})
	store := secret.NewStore("test-master-key")

	r := SyncOne(context.Background(), db, store, p.ID, "manual", SyncConfig{Adapter: f}, time.Now())
	if !r.OK || r.Added != 1 {
		t.Fatalf("scoped sync: %+v", r)
	}
	var rows []storage.CloudResource
	if err := db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].ExternalID != "r-1" {
		t.Fatalf("scoped rows: %+v err=%v", rows, err)
	}
	r = SyncOne(context.Background(), db, store, p.ID, "manual",
		SyncConfig{Adapter: f, Region: "nyc1"}, time.Now())
	if r.OK || r.Error != "region outside provider scope" {
		t.Fatalf("out-of-scope sync: %+v", r)
	}
	// Account-wide provider: a region pass reconciles only that region's rows,
	// leaving other regions' rows untouched.
	if err := db.Model(&storage.Provider{}).Where("id = ?", p.ID).Update("region", "").Error; err != nil {
		t.Fatalf("clear scope: %v", err)
	}
	r = SyncOne(context.Background(), db, store, p.ID, "manual",
		SyncConfig{Adapter: f, Region: "nyc1"}, time.Now())
	if !r.OK || r.Added != 1 {
		t.Fatalf("region pass: %+v", r)
	}
	var stillThere storage.CloudResource
	if err := db.Where("external_id = ?", "r-1").First(&stillThere).Error; err != nil || stillThere.Missing {
		t.Fatalf("out-of-region row marked missing: %+v err=%v", stillThere, err)
	}
}

// TestSyncFailuresAndLog covers F-0349: failed runs still persist a log with
// the typed error, disabled/unknown accounts and unreadable credentials fail
// closed, and RunProvider writes a run record for the configured scope.
func TestSyncFailuresAndLog(t *testing.T) {
	db := syncTestDB(t)
	p := syncTestProvider(t, db, "")
	store := secret.NewStore("test-master-key")

	r := SyncOne(context.Background(), db, store, p.ID, "manual", SyncConfig{}, time.Now())
	if r.OK || r.Error == "" {
		t.Fatalf("missing adapter should fail: %+v", r)
	}
	_, err := Finish(db, p.ID, "", "manual", r)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	var logRow storage.CloudSyncLog
	if err := db.First(&logRow).Error; err != nil || logRow.OK || logRow.Error == "" || logRow.FinishedAt.IsZero() {
		t.Fatalf("failure log: %+v err=%v", logRow, err)
	}
	r = SyncOne(context.Background(), db, store, 0, "manual", SyncConfig{}, time.Now())
	if r.OK || r.Error != "invalid provider id" {
		t.Fatalf("invalid id: %+v", r)
	}
	if err := db.Model(&storage.Provider{}).Where("id = ?", p.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	r = SyncOne(context.Background(), db, store, p.ID, "manual", SyncConfig{}, time.Now())
	if r.OK || r.Error != "provider disabled" {
		t.Fatalf("disabled provider: %+v", r)
	}
	if err := db.Model(&storage.Provider{}).Where("id = ?", p.ID).Update("enabled", true).Error; err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := db.Model(&storage.Provider{}).Where("id = ?", p.ID).Update("access_key", "v1:bad").Error; err != nil {
		t.Fatalf("corrupt key: %v", err)
	}
	r = SyncOne(context.Background(), db, store, p.ID, "manual", SyncConfig{}, time.Now())
	if r.OK || r.Error == "" {
		t.Fatalf("corrupt credential should fail: %+v", r)
	}
	// RunProvider with a wired adapter writes its own log row.
	f := cloudprov.NewFake()
	r2, err := RunProvider(context.Background(), db, store, p.ID, "scheduled", SyncConfig{Adapter: f})
	if err != nil || len(r2) != 1 || !r2[0].OK {
		t.Fatalf("RunProvider: %+v err=%v", r2, err)
	}
	var n int64
	if err := db.Model(&storage.CloudSyncLog{}).Where("trigger = ?", "scheduled").Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("scheduled log count=%d err=%v", n, err)
	}
}
