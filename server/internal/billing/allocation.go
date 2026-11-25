package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm/clause"
)

// AllocationTarget is one destination of a split (F-0561).
type AllocationTarget struct {
	Kind string `json:"kind"`
	// ID is the row id for node/path targets; Key is used for provider/account/
	// resource/region targets.
	ID  uint   `json:"id,omitempty"`
	Key string `json:"key,omitempty"`
}

// RuleParams is the payload of a versioned rule (F-0563~F-0568).
type RuleParams struct {
	// Targets is the target list for ratio/bandwidth/traffic splits.
	Targets []AllocationTarget `json:"targets,omitempty"`
	// Ratios is the fixed-ratio numerator in ppm, aligned with Targets
	// (F-0566); retained for manual edits (F-0567).
	Ratios []int64 `json:"ratios,omitempty"`
	// Category restricts the rule to one charge category ("" = all).
	Category string `json:"category,omitempty"`
	// UsageWeight picks the shared-cost basis for bandwidth/traffic rules
	// (F-0564/F-0565); values are the target keys and weights.
	UsageWeight map[string]int64 `json:"usage_weight,omitempty"`
}

// ParseRuleParams decodes a stored rule payload; blank/legacy text yields an
// empty rule rather than breaking a recompute.
func ParseRuleParams(raw string) RuleParams {
	var p RuleParams
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	return p
}

// Allocate applies one versioned rule to one bill item, splitting its
// original amount across targets. Each (item, rule version, target) row has a
// unique key, so calling Allocate twice for the same version is a no-op
// (F-0570). Recomputation uses a new version and leaves old rows intact
// (F-0571/F-0572).
func (s *Service) Allocate(itemID, ruleID uint, version int, now time.Time) ([]storage.CostAllocation, error) {
	if version <= 0 {
		return nil, errors.New("rule version must be positive")
	}
	var item storage.CostItem
	if err := s.db.First(&item, itemID).Error; err != nil {
		return nil, err
	}
	var rule storage.CostAllocationRule
	if err := s.db.First(&rule, ruleID).Error; err != nil {
		return nil, err
	}
	if rule.Version != version {
		return nil, fmt.Errorf("rule version mismatch: stored=%d requested=%d", rule.Version, version)
	}
	params := ParseRuleParams(rule.Params)
	if params.Category != "" && normalizeCategory(params.Category) != item.Category {
		return nil, nil
	}
	targets, weights, err := splitTargets(rule.Kind, params, item)
	if err != nil {
		return nil, err
	}
	itemKey := item.DedupeKey
	// Integer division drops remainder cents on weighted splits; spread them
	// across targets in order so the split sums exactly to the original
	// amount (F-0569).
	amounts := make([]int64, len(targets))
	var sum int64
	for i, w := range weights {
		if rule.Kind == BasisDirect {
			amounts[i] = item.AmountCents
		} else {
			amounts[i] = item.AmountCents * w / 1_000_000
		}
		sum += amounts[i]
	}
	if rule.Kind != BasisDirect && sum != item.AmountCents && len(amounts) > 0 {
		amounts[len(amounts)-1] += item.AmountCents - sum
	}
	out := make([]storage.CostAllocation, 0, len(targets))
	for i, target := range targets {
		if !ValidTarget(target.Kind) {
			return nil, fmt.Errorf("invalid allocation target kind %q", target.Kind)
		}
		targetKey := target.Key
		if targetKey == "" {
			targetKey = fmt.Sprintf("id:%d", target.ID)
		}
		ratio := int64(0)
		if rule.Kind == BasisRatio && i < len(params.Ratios) {
			ratio = params.Ratios[i]
		}
		allocated := amounts[i]
		row := storage.CostAllocation{
			CostItemID: item.ID, RuleID: rule.ID, RuleVersion: version,
			TargetKind: target.Kind, TargetID: target.ID, TargetKey: targetKey,
			Basis: rule.Kind, RatioPPM: ratio,
			OriginalCents: item.AmountCents, AllocatedCents: allocated,
			DedupeKey: AllocationKey(itemKey, rule.ID, version, target.Kind, targetKey),
		}
		res := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&row)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected > 0 {
			out = append(out, row)
		}
	}
	return out, nil
}

// splitTargets resolves the targets and their ppm weights for one rule kind.
// Weights always sum to 1_000_000 so an item is split exactly once; a manual
// rule uses an explicit ratio (ppm) supplied by the operator.
func splitTargets(kind string, params RuleParams, item storage.CostItem) ([]AllocationTarget, []int64, error) {
	if kind == BasisDirect {
		key := item.ResourceID
		if item.NodeID != nil {
			return []AllocationTarget{{Kind: TargetNode, ID: *item.NodeID}}, []int64{1_000_000}, nil
		}
		if key == "" {
			key = "unattributed"
		}
		return []AllocationTarget{{Kind: TargetResource, Key: key}}, []int64{1_000_000}, nil
	}
	if len(params.Targets) == 0 {
		return nil, nil, errors.New("allocation rule has no targets")
	}
	switch kind {
	case BasisRatio:
		if len(params.Ratios) != len(params.Targets) {
			return nil, nil, errors.New("ratio count must match target count")
		}
		var total int64
		for _, r := range params.Ratios {
			if r < 0 {
				return nil, nil, errors.New("ratio must not be negative")
			}
			total += r
		}
		if total != 1_000_000 {
			return nil, nil, fmt.Errorf("ratios must total 1000000 ppm, got %d", total)
		}
		return params.Targets, params.Ratios, nil
	case BasisBandwidth, BasisTraffic, BasisManual:
		weights := make([]int64, len(params.Targets))
		var total int64
		for i, t := range params.Targets {
			key := t.Key
			if key == "" {
				key = fmt.Sprintf("id:%d", t.ID)
			}
			weight := params.UsageWeight[key]
			if weight <= 0 {
				return nil, nil, fmt.Errorf("missing positive weight for target %q", key)
			}
			weights[i] = weight
			total += weight
		}
		// Convert raw usage weights to ppm using integer math; distribute the
		// remainder so the weights sum to exactly 1_000_000.
		out := make([]int64, len(weights))
		var assigned int64
		for i, w := range weights {
			out[i] = w * 1_000_000 / total
			assigned += out[i]
		}
		if assigned != 1_000_000 && len(out) > 0 {
			out[len(out)-1] += 1_000_000 - assigned
		}
		return params.Targets, out, nil
	default:
		return nil, nil, fmt.Errorf("unknown allocation basis %q", kind)
	}
}

// Recompute allocates every item of the given record under a new rule version.
// Historical rows are untouched; duplicate allocations under the new version
// are skipped (F-0571/F-0572).
func (s *Service) Recompute(recordID, ruleID uint, version int, now time.Time) (int, error) {
	var items []storage.CostItem
	if err := s.db.Where("cost_record_id = ?", recordID).Find(&items).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		rows, err := s.Allocate(item.ID, ruleID, version, now)
		if err != nil {
			return count, err
		}
		count += len(rows)
	}
	return count, nil
}

// Reconcile builds one estimate-vs-actual row for a scope and period (F-0587).
// Passing a zero actual leaves the row open; a later update fills it without
// touching the underlying bill records (F-0572).
func (s *Service) Reconcile(in storage.CostReconciliation, now time.Time) (*storage.CostReconciliation, error) {
	if !ValidTarget(in.ScopeKind) || in.Currency == "" {
		return nil, errors.New("scope kind and currency are required")
	}
	if in.PeriodStart.IsZero() || in.PeriodEnd.IsZero() || in.PeriodEnd.Before(in.PeriodStart) {
		return nil, errors.New("invalid reconciliation period")
	}
	target := in.TargetKey
	if target == "" {
		target = fmt.Sprintf("id:%d", in.TargetID)
	}
	in.DedupeKey = ReconciliationKey(in.ScopeKind, target, in.PeriodStart, in.PeriodEnd)
	in.DiffCents = in.ActualCents - in.EstimateCents
	if in.Status == "" {
		in.Status = "open"
	}
	res := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true}).Create(&in)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		if err := s.db.Model(&storage.CostReconciliation{}).Where("dedupe_key = ?", in.DedupeKey).
			Updates(map[string]any{
				"estimate_cents": in.EstimateCents, "actual_cents": in.ActualCents,
				"diff_cents": in.DiffCents, "updated_at": now,
			}).Error; err != nil {
			return nil, err
		}
	}
	var out storage.CostReconciliation
	if err := s.db.Where("dedupe_key = ?", in.DedupeKey).First(&out).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// ConfirmReconciliation records the operator's difference confirmation and
// optional approval (F-0591/F-0592).
func (s *Service) ConfirmReconciliation(id uint, status, note, actor string) (*storage.CostReconciliation, error) {
	if status != "confirmed" && status != "adjusted" && status != "open" {
		return nil, fmt.Errorf("invalid reconciliation status %q", status)
	}
	var row storage.CostReconciliation
	if err := s.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	updates := map[string]any{"status": status, "note": note, "approved_by": actor}
	if err := s.db.Model(&row).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := s.db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// NewVersion appends a new version of an existing rule (F-0568): the old row
// stays history, the new row is the only one recomputation may use.
func (s *Service) NewVersion(ruleID uint, params RuleParams, note string, now time.Time) (*storage.CostAllocationRule, error) {
	var old storage.CostAllocationRule
	if err := s.db.First(&old, ruleID).Error; err != nil {
		return nil, err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	next := storage.CostAllocationRule{
		Name: old.Name, Kind: old.Kind, Version: old.Version + 1,
		Enabled: true, Params: string(raw), Note: note,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.Create(&next).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&storage.CostAllocationRule{}).Where("id = ?", old.ID).Update("enabled", false).Error; err != nil {
		return nil, err
	}
	return &next, nil
}
