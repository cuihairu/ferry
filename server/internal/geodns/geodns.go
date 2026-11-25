// Package geodns implements smart-DNS per-region routing (E-27, entries and load balancing design §C.8):
// ferry does not run an authoritative DNS of its own or do geographic resolution (that lives in the GeoDNS
// provider's line library) - this package only picks a region's representative entry (same policy as E-26
// orderByProbe) and reconciles {region-slug}.{front-domain} A records event-driven (idempotent Upsert,
// full-outage record withdrawal; no polling loop is built, and the pool attach/detach hooks are the
// slot for it), reusing the DNSProvider credential channel (R24 encrypted surface).
// A synchronized subdomain is never written back to the manual entry_domains list: per-region routing is an addition, not a dependency, and when no nearby domain is available the subscription list self-selects a fallback.
package geodns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// rttWindow matches the subscription-side E-19 policy (sub.go entryRttMs): the mean RTT of reachable tunnel
// probes within the window participates in representative-entry selection.
const rttWindow = 30 * time.Minute

// Syncer is the executor of one per-region reconciliation; New can inject a fake channel (for tests).
type Syncer struct {
	DB    *gorm.DB
	Store *secret.Store
	New   func(kind, token string) (dns.Provider, error)
}

func (s *Syncer) factory() func(kind, token string) (dns.Provider, error) {
	if s.New != nil {
		return s.New
	}
	return dns.New
}

// RegionReps picks a representative entry per region (§C.8.2): nodes(entry/both)+active in the pool+
// non-provisioning (sick is already out via removal), 30min reachable-tunnel RTT aggregation, group RTTs in
// ascending order with no data trailing last, and stable ordering for ties (id ascending) - the same
// policy as E-26 orderByProbe but only taking each group's first entry. The region key is nodes.region's
// original value; regions with no entry do not appear in the current results (full-outage record
func (s *Syncer) RegionReps(now time.Time) (map[string]storage.Node, error) {
	var nodes []storage.Node
	if err := s.DB.Where("enabled = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
		true, "entry", "both", pool.StateActive, "provisioning").
		Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	rtt := s.entryRttMs(now)
	groups := map[string][]storage.Node{}
	for _, n := range nodes {
		key := n.Region
		if key == "" {
			key = "未知"
		}
		groups[key] = append(groups[key], n)
	}
	reps := make(map[string]storage.Node, len(groups))
	for region, ns := range groups {
		sort.SliceStable(ns, func(i, j int) bool {
			ri, rj := rtt[ns[i].ID], rtt[ns[j].ID]
			ti, tj := tierOf(ri), tierOf(rj)
			if ti != tj {
				return ti < tj
			}
			if ti == 0 && ri != rj { // data tiers ascending by RTT
				return ri < rj
			}
			return ns[i].ID < ns[j].ID // stable within a tier: by id
		})
		reps[region] = ns[0]
	}
	return reps, nil
}

// tierOf 0=has probe data (known RTT), 1=no data, trailing (same policy as E-26 probeTier).
func tierOf(rtt int) int {
	if rtt > 0 {
		return 0
	}
	return 1
}

// entryRttMs aggregates the mean RTT of reachable tunnel probes within the window (same query as sub.go).
func (s *Syncer) entryRttMs(now time.Time) map[uint]int {
	out := map[uint]int{}
	var rows []struct {
		NodeID uint
		Avg    float64
	}
	if err := s.DB.Model(&storage.ProbeReport{}).
		Select("node_id, AVG(rtt_ms) AS avg").
		Where("target_kind = ? AND reachable = ? AND probed_at > ?", "tunnel", true, now.Add(-rttWindow)).
		Group("node_id").Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.NodeID] = int(r.Avg + 0.5)
	}
	return out
}

// Slug normalizes the region vocabulary (§C.8.3): lowercase, whitespace and underscores to hyphens,
// keeping only [a-z0-9-]; when normalization leaves it empty (e.g. a purely Chinese region name) no
// record is produced - the region vocabulary is recommended to be ASCII slugs (same source as the manual entry_domains.region list).
func Slug(region string) string {
	s := strings.ToLower(strings.TrimSpace(region))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.Join(strings.Fields(s), "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// Sync reconciles (§C.8.4): under each enabled DNS vendor's front domain, it aligns the
// {slug}.{domain} A record to the representative-entry set - Upsert on add/change, Delete on
// disappearance (full-outage record withdrawal); the synchronized face lands in GeoDNSRecord as the
// baseline for the next round's diff. Changes leave a trail in the admin log, and a single failure
// alerts via herald (per-day dedup) then continues with the remaining records.
func (s *Syncer) Sync(ctx context.Context) (int, error) {
	var provs []storage.DNSProvider
	if err := s.DB.Where("enabled = ?", true).Find(&provs).Error; err != nil {
		return 0, err
	}
	if len(provs) == 0 {
		return 0, nil
	}
	ids := make([]uint, 0, len(provs))
	for _, p := range provs {
		ids = append(ids, p.ID)
	}
	var fronts []storage.DNSFront
	if err := s.DB.Where("provider_id IN ?", ids).Find(&fronts).Error; err != nil {
		return 0, err
	}
	if len(fronts) == 0 {
		return 0, nil
	}
	if s.Store == nil || !s.Store.Enabled() {
		return 0, errors.New("geodns: secret master key not configured (set FERRY_SECRET_KEY)")
	}
	channels := map[uint]dns.Provider{}
	for _, p := range provs {
		token, err := s.Store.Decrypt(p.APIKey)
		if err != nil {
			return 0, fmt.Errorf("geodns: dns provider %s: %w", p.Name, err)
		}
		ch, err := s.factory()(p.Type, token)
		if err != nil {
			return 0, fmt.Errorf("geodns: dns provider %s: %w", p.Name, err)
		}
		channels[p.ID] = ch
	}
	reps, err := s.RegionReps(time.Now())
	if err != nil {
		return 0, err
	}
	var existing []storage.GeoDNSRecord
	if err := s.DB.Find(&existing).Error; err != nil {
		return 0, err
	}
	exByKey := make(map[string]storage.GeoDNSRecord, len(existing))
	for _, r := range existing {
		exByKey[fmt.Sprintf("%d/%s", r.FrontID, r.Slug)] = r
	}
	now := time.Now()
	changes := 0
	var firstErr error
	fail := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		if firstErr == nil {
			firstErr = err
		}
		emitSyncFailed(s.DB, err)
	}
	for _, f := range fronts {
		ch := channels[f.ProviderID]
		want := map[string]string{}
		for region, n := range reps {
			sl := Slug(region)
			if sl == "" {
				continue // region vocabulary is not an ASCII slug, no record produced
			}
			want[sl] = n.Address
		}
		for sl, val := range want {
			key := fmt.Sprintf("%d/%s", f.ID, sl)
			if rec, ok := exByKey[key]; ok && rec.Value == val {
				continue // no change, idempotently skip
			}
			name := sl + "." + f.Domain
			if err := ch.Upsert(ctx, name, "A", val); err != nil {
				fail("geodns: upsert %s: %w", name, err)
				continue
			}
			rec := storage.GeoDNSRecord{FrontID: f.ID, Slug: sl, Name: name, Value: val, UpdatedAt: now}
			if prev, ok := exByKey[key]; ok {
				rec.ID = prev.ID
			}
			if err := s.DB.Save(&rec).Error; err != nil {
				fail("geodns: save record %s: %w", name, err)
				continue
			}
			changes++
		}
		// Record withdrawal: regions under this front domain in the synchronized face with no representative entry any more.
		for _, r := range existing {
			if r.FrontID != f.ID {
				continue
			}
			if _, ok := want[r.Slug]; ok {
				continue
			}
			if err := ch.Delete(ctx, r.Name, "A"); err != nil {
				fail("geodns: delete %s: %w", r.Name, err)
				continue
			}
			if err := s.DB.Delete(&storage.GeoDNSRecord{}, r.ID).Error; err != nil {
				fail("geodns: drop record %s: %w", r.Name, err)
				continue
			}
			changes++
		}
	}
	if changes > 0 {
		ringlog.Default().Add(fmt.Sprintf("geodns: 分地域对账 %d 条记录变更", changes))
	}
	return changes, firstErr
}

// emitSyncFailed is a reconciliation failure alert: at most one per local day (dedup fallback, same policy as
// price_alert); when Herald is unconfigured it lands in the local outbox, visible in-app.
func emitSyncFailed(db *gorm.DB, cause error) {
	key := fmt.Sprintf("geodns_sync:%s", time.Now().Format("2006-01-02"))
	var n int64
	if err := db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindGeoSyncFailed, key).Count(&n).Error; err != nil {
		log.Printf("geodns alert: %v", err)
		return
	}
	if n > 0 {
		return
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind:     herald.KindGeoSyncFailed,
		Severity: herald.SeverityWarning,
		Title:    "智能 DNS 分地域对账失败",
		Body:     cause.Error() + "（分地域记录未对齐，订阅自选优不受影响）",
		Target:   herald.TargetAdmin,
		DedupKey: key,
	}); err != nil {
		log.Printf("geodns alert: %v", err)
	}
}
