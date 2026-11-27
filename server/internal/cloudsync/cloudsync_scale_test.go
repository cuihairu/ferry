package cloudsync

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestCloudSyncScale drives the real reconciliation path (F-2068) at 3000
// vendor resources: first pull inserts every row exactly once even when the
// vendor list repeats ids, an unchanged re-pull updates without growing the
// table, and a changed pull applies state changes and marks vanished
// resources missing without deleting their bindings. It also runs a batch of
// independent provider accounts concurrently through the registered factory
// seam to cover multi-account sync load. Numbers are archived in
// docs/testing/scale-report.md.
func TestCloudSyncScale(t *testing.T) {
	const resources = 3000
	db := syncTestDB(t)
	p := syncTestProvider(t, db, "")
	store := secret.NewStore("test-master-key")
	f := cloudprov.NewFake()
	now := time.Now()

	regions := []string{"fra1", "nyc1", "sgp1"}
	for i := 0; i < resources; i++ {
		f.Seed(cloudprov.Resource{
			ResourceID: fmt.Sprintf("res-%05d", i),
			Name:       fmt.Sprintf("node-%05d", i),
			Region:     regions[i%len(regions)],
			State:      cloudprov.StateRunning,
			PublicIP:   fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256),
			BwMbps:     i%1000 + 1,
			Spec:       "vc2-1c-1gb",
			Tags:       map[string]string{"ferry": "managed"},
		})
	}
	// Vendor pagination overlap: two extra copies of an existing id must not
	// create duplicate rows at volume (F-0352).
	f.Seed(cloudprov.Resource{ResourceID: "res-00000", Name: "overlap-a", Region: "fra1", State: cloudprov.StateRunning})
	f.Seed(cloudprov.Resource{ResourceID: "res-00000", Name: "overlap-b", Region: "fra1", State: cloudprov.StateRunning})

	start := time.Now()
	r1, log1, err := SyncAndLog(context.Background(), db, store, p.ID, "scale-first", SyncConfig{Adapter: f}, now)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	firstElapsed := time.Since(start)
	if !r1.OK || r1.Added != resources || r1.Updated != 0 || r1.Missing != 0 {
		t.Fatalf("first sync result: %+v", r1)
	}
	if log1 == nil || !log1.OK || log1.Added != resources {
		t.Fatalf("first sync log: %+v", log1)
	}
	assertResourceCount(t, db, p.ID, resources)

	// Bind a sample row before the second pass: the missing marker must not
	// destroy bindings at volume (F-0351).
	nodeID := uint(4242)
	if err := db.Model(&storage.CloudResource{}).Where("external_id = ?", "res-00001").
		Update("node_id", nodeID).Error; err != nil {
		t.Fatalf("bind: %v", err)
	}

	start = time.Now()
	r2, _, err := SyncAndLog(context.Background(), db, store, p.ID, "scale-repeat", SyncConfig{Adapter: f}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("repeat sync: %v", err)
	}
	repeatElapsed := time.Since(start)
	if !r2.OK || r2.Added != 0 || r2.Updated != resources || r2.Missing != 0 {
		t.Fatalf("repeat sync result: %+v", r2)
	}
	assertResourceCount(t, db, p.ID, resources)

	// Third pass: half the fleet vanishes at the vendor, one survives with a
	// changed state. Missing is a marker, never a delete.
	changed := 0
	for i := 0; i < resources; i++ {
		id := fmt.Sprintf("res-%05d", i)
		if i%2 == 0 {
			if !f.Unseed(id) {
				t.Fatalf("unseed %s", id)
			}
			continue
		}
		if i == 1 {
			f.Seed(cloudprov.Resource{ResourceID: id, Name: "node-00001", Region: regions[i%len(regions)],
				State: cloudprov.StateStopped, BwMbps: 999})
			changed++
		}
	}
	start = time.Now()
	r3, _, err := SyncAndLog(context.Background(), db, store, p.ID, "scale-drift", SyncConfig{Adapter: f}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("drift sync: %v", err)
	}
	driftElapsed := time.Since(start)
	wantMissing := resources / 2
	if !r3.OK || r3.Added != 0 || r3.Missing != wantMissing || r3.Updated != resources-wantMissing {
		t.Fatalf("drift sync result: %+v (changed=%d)", r3, changed)
	}
	assertResourceCount(t, db, p.ID, resources)
	var bound storage.CloudResource
	if err := db.Where("external_id = ?", "res-00001").First(&bound).Error; err != nil {
		t.Fatalf("bound row gone: %v", err)
	}
	if bound.State != cloudprov.StateStopped || bound.BwMbps != 999 || bound.Missing {
		t.Fatalf("changed row not updated: %+v", bound)
	}
	var vanished storage.CloudResource
	if err := db.Where("external_id = ?", "res-00000").First(&vanished).Error; err != nil {
		t.Fatalf("missing row deleted: %v", err)
	}
	if !vanished.Missing || vanished.MissingSince == nil {
		t.Fatalf("vanished row not marked: %+v", vanished)
	}
	var logCount int64
	if err := db.Model(&storage.CloudSyncLog{}).Where("provider_id = ?", p.ID).Count(&logCount).Error; err != nil || logCount != 3 {
		t.Fatalf("sync logs=%d want 3 err=%v", logCount, err)
	}

	// Multi-account load: 20 providers, 100 resources each, all through the
	// registered adapter factory (not the direct cfg.Adapter seam).
	const accounts, perAccount = 20, 100
	providerIDs := make([]uint, 0, accounts)
	adapters := make(map[string]*cloudprov.Fake, accounts)
	for i := 0; i < accounts; i++ {
		typ := fmt.Sprintf("scale-type-%02d", i)
		ad := cloudprov.NewFake()
		for j := 0; j < perAccount; j++ {
			ad.Seed(cloudprov.Resource{ResourceID: fmt.Sprintf("acct-%02d-res-%03d", i, j),
				Name: "acct", Region: "fra1", State: cloudprov.StateRunning})
		}
		adapters[typ] = ad
		cloudprov.Register(typ, func(a cloudprov.Account) (cloudprov.Adapter, error) {
			return adapters[a.Type], nil
		})
		t.Cleanup(func() { cloudprov.Unregister(typ) })
		sealed, err := secret.NewStore("test-master-key").Encrypt("sk-scale")
		if err != nil {
			t.Fatal(err)
		}
		ap := storage.Provider{Name: fmt.Sprintf("scale-%02d", i), Type: typ, AccessKey: sealed, Enabled: true}
		if err := db.Create(&ap).Error; err != nil {
			t.Fatal(err)
		}
		providerIDs = append(providerIDs, ap.ID)
	}

	start = time.Now()
	var wg sync.WaitGroup
	errCh := make(chan error, accounts)
	for i := 0; i < accounts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var provider storage.Provider
			if err := db.Where("name = ?", fmt.Sprintf("scale-%02d", i)).First(&provider).Error; err != nil {
				errCh <- err
				return
			}
			r, _, err := SyncAndLog(context.Background(), db, store, provider.ID, "scale-accounts", SyncConfig{}, time.Now())
			if err != nil {
				errCh <- err
			} else if !r.OK || r.Added != perAccount {
				errCh <- fmt.Errorf("account %d: %+v", i, r)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent sync: %v", err)
	}
	multiElapsed := time.Since(start)
	assertResourceCount(t, db, 0, resources+accounts*perAccount)
	for _, id := range providerIDs {
		assertResourceCount(t, db, id, perAccount)
	}

	t.Logf("cloudsync scale: %d resources first %s, repeat %s, drift %s; %d accounts×%d resources %s",
		resources, firstElapsed, repeatElapsed, driftElapsed, accounts, perAccount, multiElapsed)
	if firstElapsed > 60*time.Second || multiElapsed > 60*time.Second {
		t.Fatalf("scale pass outside CI budget: first %s multi %s", firstElapsed, multiElapsed)
	}
}

func assertResourceCount(t *testing.T, db *gorm.DB, providerID uint, want int) {
	t.Helper()
	q := db.Model(&storage.CloudResource{})
	if providerID != 0 {
		q = q.Where("provider_id = ?", providerID)
	}
	var got int64
	if err := q.Count(&got).Error; err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if int(got) != want {
		t.Fatalf("resources=%d want %d", got, want)
	}
}
