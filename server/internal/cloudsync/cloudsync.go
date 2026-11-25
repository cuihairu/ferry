// Package cloudsync (F-0343~F-0352) pulls provider resources through the
// cloudprov Adapter contract and reconciles them into storage.CloudResource
// rows: one account/region at a time (F-0344), vendor tag filtering (F-0345),
// incremental scope (F-0348) via provider.Region, upsert dedup by
// (provider_id, external_id) (F-0352), and a soft "missing" marker for
// provider-side deletion (F-0350/F-0351) -- a sync never deletes a bound
// Node's history. Every run writes a CloudSyncLog with counts and timing
// (F-0349). The scheduled loop (F-0347) and manual handler endpoint
// (F-0346) call the same SyncOne function.
package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// SyncConfig identifies an account and (optionally) one region scope.
type SyncConfig struct {
	// Region narrows the pull to one vendor region (F-0344). Empty means
	// the account-wide scope; Provider.Region (F-0324) is applied on top as
	// the account's authorization boundary (F-0334) when configured.
	Region string
	// Tags is the vendor-side tag filter (F-0345); an empty map lists all.
	Tags map[string]string
	// Adapter, when set, overrides the registered factory (test seam).
	Adapter cloudprov.Adapter
}

// Result is one run's reconciliation summary (F-0349).
type Result struct {
	OK       bool
	Added    int
	Updated  int
	Missing  int
	Error    string
	Started  time.Time
	Finished time.Time
}

// SyncOne pulls one account scope and upserts its resources. It returns the
// result even on vendor failure so callers can always persist the run log.
func SyncOne(ctx context.Context, db *gorm.DB, store *secret.Store, providerID uint, trigger string, cfg SyncConfig, now time.Time) (r Result) {
	r = Result{Started: now}
	defer func() {
		r.Finished = time.Now()
	}()
	if providerID == 0 {
		r.Error = "invalid provider id"
		return r
	}
	var p storage.Provider
	if err := db.First(&p, providerID).Error; err != nil {
		r.Error = fmt.Sprintf("load provider: %v", err)
		return r
	}
	if !p.Enabled {
		r.Error = "provider disabled"
		return r
	}
	var a cloudprov.Adapter
	if cfg.Adapter != nil {
		a = cfg.Adapter
	} else {
		factory := cloudprov.Lookup(p.Type)
		if factory == nil {
			r.Error = "no adapter registered for type " + p.Type
			return r
		}
		plain, err := store.Decrypt(p.AccessKey)
		if err != nil {
			r.Error = "credential unreadable: " + err.Error()
			return r
		}
		a, err = factory(cloudprov.Account{
			ProviderID: p.ID, Name: p.Name, Type: p.Type,
			AccessKey: plain, Region: p.Region,
		})
		if err != nil {
			r.Error = fmt.Sprintf("adapter init: %v", err)
			return r
		}
	}
	// The account scope is authoritative (F-0334): a configured Provider.Region
	// intersects the requested region; a mismatch pulls nothing instead of
	// silently widening the credential's authorization boundary.
	region := cfg.Region
	if p.Region != "" {
		if region != "" && region != p.Region {
			r.Error = "region outside provider scope"
			return r
		}
		region = p.Region
	}
	client := cloudprov.NewClient(a, cloudprov.Defaults)
	flt := cloudprov.Filter{Region: region, Tags: cfg.Tags}
	list, err := client.ListResources(ctx, flt)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	// Normalize once: stable ordering and dedup by external id protect against
	// vendor pagination overlap and repeated imports (F-0348/F-0352).
	sort.Slice(list, func(i, j int) bool { return list[i].ResourceID < list[j].ResourceID })
	seen := map[string]bool{}
	touchedIDs := []uint{}
	var unique []cloudprov.Resource
	for _, item := range list {
		if item.ResourceID == "" || seen[item.ResourceID] {
			continue
		}
		seen[item.ResourceID] = true
		unique = append(unique, item)
	}
	// Vendor tags are persisted as JSON (F-0345); failures fall back to no
	// tags rather than blocking the whole sync.
	tagJSON := func(tags map[string]string) string {
		if len(tags) == 0 {
			return ""
		}
		b, err := json.Marshal(tags)
		if err != nil {
			return ""
		}
		return string(b)
	}
	var rows []storage.CloudResource
	if err := db.Where("provider_id = ?", providerID).Find(&rows).Error; err != nil {
		r.Error = fmt.Sprintf("load resources: %v", err)
		return r
	}
	byExternal := make(map[string]storage.CloudResource, len(rows))
	for _, row := range rows {
		byExternal[row.ExternalID] = row
	}
	// A scoped run must not mark out-of-scope rows missing (F-0344/F-0348).
	scopeRegion := region
	touched := map[string]bool{}
	for _, item := range unique {
		touched[item.ResourceID] = true
		existing, found := byExternal[item.ResourceID]
		if found {
			touchedIDs = append(touchedIDs, existing.ID)
			updates := map[string]any{
				"name": item.Name, "state": item.State, "public_ip": item.PublicIP,
				"bw_mbps": item.BwMbps, "spec": item.Spec, "tags": tagJSON(item.Tags),
				"last_seen_at": now, "missing": false, "missing_since": nil, "last_sync_at": now,
			}
			if scopeRegion != "" {
				updates["region"] = scopeRegion
			}
			if err := db.Model(&storage.CloudResource{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
				r.Error = fmt.Sprintf("update resource: %v", err)
				return r
			}
			r.Updated++
			continue
		}
		row := storage.CloudResource{
			ProviderID: providerID, ExternalID: item.ResourceID, Region: item.Region,
			Name: item.Name, State: item.State, PublicIP: item.PublicIP,
			BwMbps: item.BwMbps, Spec: item.Spec, Tags: tagJSON(item.Tags),
			LastSeenAt: now,
		}
		if scopeRegion != "" {
			row.Region = scopeRegion
		}
		if err := db.Create(&row).Error; err != nil {
			r.Error = fmt.Sprintf("insert resource: %v", err)
			return r
		}
		// The created row's id is backfilled by GORM; stamping needs it.
		touchedIDs = append(touchedIDs, row.ID)
		r.Added++
	}
	// Provider-side disappearance: mark, never delete. Node bindings and
	// history remain intact (F-0351); a reappearing resource clears the flag.
	for external, row := range byExternal {
		if touched[external] || row.Missing {
			continue
		}
		if scopeRegion != "" && row.Region != scopeRegion {
			continue
		}
		updates := map[string]any{"missing": true, "missing_since": now}
		if err := db.Model(&storage.CloudResource{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			r.Error = fmt.Sprintf("mark missing: %v", err)
			return r
		}
		r.Missing++
	}
	// Per-resource last sync stamp (F-0349); the run-level log is separate.
	if len(touchedIDs) > 0 {
		if err := db.Model(&storage.CloudResource{}).Where("id IN ?", touchedIDs).
			Update("last_sync_at", now).Error; err != nil {
			r.Error = fmt.Sprintf("stamp sync: %v", err)
			return r
		}
	}
	r.OK = true
	return r
}

// Finish writes one run log row and returns it (F-0349).
func Finish(db *gorm.DB, providerID uint, region, trigger string, r Result) (*storage.CloudSyncLog, error) {
	row := &storage.CloudSyncLog{
		ProviderID: providerID, Region: region, Trigger: trigger,
		OK: r.OK, Added: r.Added, Updated: r.Updated, Missing: r.Missing,
		Error: r.Error, StartedAt: r.Started, FinishedAt: r.Finished,
	}
	if err := db.Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// SyncAndLog runs SyncOne and persists its log (F-0346/F-0347).
func SyncAndLog(ctx context.Context, db *gorm.DB, store *secret.Store, providerID uint, trigger string, cfg SyncConfig, now time.Time) (Result, *storage.CloudSyncLog, error) {
	r := SyncOne(ctx, db, store, providerID, trigger, cfg, now)
	logRow, err := Finish(db, providerID, cfg.Region, trigger, r)
	return r, logRow, err
}

// RunProvider syncs one account (F-0344): a configured Provider.Region narrows
// the run to that scope, otherwise the pull is account-wide. Manual or
// scheduled callers that want a region pass supply SyncConfig.Region, which
// SyncOne isolates so a scoped run never marks other regions' rows missing.
func RunProvider(ctx context.Context, db *gorm.DB, store *secret.Store, providerID uint, trigger string, opts SyncConfig) ([]Result, error) {
	var p storage.Provider
	if err := db.First(&p, providerID).Error; err != nil {
		return nil, err
	}
	cfg := opts
	if p.Region != "" {
		cfg.Region = p.Region
	}
	r, _, err := SyncAndLog(ctx, db, store, providerID, trigger, cfg, time.Now())
	return []Result{r}, err
}

// Sweep syncs all enabled providers (F-0347). It continues past a failed
// account so one bad credential does not block the rest; errors are joined.
func Sweep(ctx context.Context, db *gorm.DB, store *secret.Store, trigger string) error {
	var providers []storage.Provider
	if err := db.Where("enabled = ?", true).Order("id ASC").Find(&providers).Error; err != nil {
		return err
	}
	var errs []error
	for _, p := range providers {
		if ctx.Err() != nil {
			break
		}
		if _, err := RunProvider(ctx, db, store, p.ID, trigger, SyncConfig{}); err != nil {
			errs = append(errs, fmt.Errorf("provider %d: %w", p.ID, err))
		}
	}
	return errors.Join(errs...)
}

// Loop runs Sweep on the interval until ctx is cancelled (F-0347), mirroring
// the other panel loops (toucher/review/notifyscan).
func Loop(ctx context.Context, db *gorm.DB, store *secret.Store, interval time.Duration, trigger string) {
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := Sweep(ctx, db, store, trigger); err != nil {
				fmt.Printf("cloudsync: %v\n", err)
			}
		}
	}
}
