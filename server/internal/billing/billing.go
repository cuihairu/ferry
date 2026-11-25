// Package billing stores and allocates the vendor's actual cloud bills. It is
// deliberately separate from the internal cost estimator: a cost estimate is
// never presented as a settled bill, and a bill is never reconstructed from
// the current instance count times a unit price (todo §4 header).
package billing

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Cost data-source classes (F-0502/F-0523/F-0524/F-0534).
const (
	SourceAPI    = "api"    // vendor billing API
	SourceFile   = "file"   // uploaded vendor bill file
	SourceManual = "manual" // hand-entered cost record
)

// Import triggers (F-0525~F-0528).
const (
	TriggerManual   = "manual"
	TriggerSchedule = "scheduled"
	TriggerBackfill = "backfill"
)

// Bill states (F-0597).
const (
	StatusUnsettled = "unsettled"
	StatusSettled   = "settled"
)

// Import-run states (F-0532).
const (
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunPartial   = "partial"
)

// Charge categories keep item-level costs explainable (F-0544~F-0549).
const (
	CategoryInstance  = "instance"
	CategoryTraffic   = "traffic"
	CategoryBandwidth = "bandwidth"
	CategoryIP        = "ip"
	CategoryDisk      = "disk"
	CategorySnapshot  = "snapshot"
	CategoryBackup    = "backup"
	CategoryDiscount  = "discount"
	CategoryOther     = "other"
)

// Allocation attribution kinds (F-0561).
const (
	TargetProvider = "provider"
	TargetAccount  = "account"
	TargetResource = "resource"
	TargetNode     = "node"
	TargetRegion   = "region"
	TargetPath     = "path"
)

// Allocation bases (F-0562~F-0567).
const (
	BasisDirect    = "direct"
	BasisBandwidth = "bandwidth"
	BasisTraffic   = "traffic"
	BasisRatio     = "ratio"
	BasisManual    = "manual"
)

// Estimate kinds (F-0542~F-0551).
const (
	KindInstance  = "instance"
	KindHourly    = "hourly"
	KindTraffic   = "traffic"
	KindBandwidth = "bandwidth"
	KindIP        = "ip"
	KindDisk      = "disk"
	KindSnapshot  = "snapshot"
	KindBackup    = "backup"
	KindDiscount  = "discount"
	KindCustom    = "custom"
)

// ValidSource / ValidTrigger guard the API surface; unknown values are
// rejected instead of being silently stored as a new class.
func ValidSource(s string) bool {
	return s == SourceAPI || s == SourceFile || s == SourceManual
}

func ValidTrigger(s string) bool {
	return s == TriggerManual || s == TriggerSchedule || s == TriggerBackfill
}

func ValidBasis(s string) bool {
	switch s {
	case BasisDirect, BasisBandwidth, BasisTraffic, BasisRatio, BasisManual:
		return true
	}
	return false
}

func ValidTarget(s string) bool {
	switch s {
	case TargetProvider, TargetAccount, TargetResource, TargetNode, TargetRegion, TargetPath:
		return true
	}
	return false
}

// RecordKey is the bill dedupe key (F-0512): source + vendor bill id +
// revision. A reissued bill has a new revision and therefore a new row, so the
// original charge stays available for traceability (F-0530/F-0531).
func RecordKey(source string, costProviderID uint, billID, revision string) string {
	if revision == "" {
		revision = "0"
	}
	return fmt.Sprintf("%s:%d:%s:%s", source, costProviderID, billID, revision)
}

// ItemKey is the stable key of one item inside a bill row (F-0512/F-0570).
func ItemKey(recordKey, itemID string) string {
	return recordKey + "|item:" + itemID
}

// AllocationKey is the stable key of one allocation target under one rule
// version (F-0570). A recompute creates a new version, so it never collides
// with historical allocation rows (F-0571).
func AllocationKey(itemKey string, ruleID uint, version int, targetKind, target string) string {
	return fmt.Sprintf("%s|alloc:%d:v%d:%s:%s", itemKey, ruleID, version, targetKind, target)
}

// EstimateKey is the stable key of one estimate scope/kind/period (F-0550).
func EstimateKey(kind string, nodeID, pathID *uint, region string, from, to time.Time) string {
	var node, path string
	if nodeID != nil {
		node = fmt.Sprintf("n%d", *nodeID)
	}
	if pathID != nil {
		path = fmt.Sprintf("p%d", *pathID)
	}
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s", kind, node, path, region,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
}

// ReconciliationKey is the stable key for one scope/period comparison.
func ReconciliationKey(kind, target string, from, to time.Time) string {
	return fmt.Sprintf("%s:%s:%s:%s", kind, target,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
}

// RawJSON is the traceability serializer for vendor payloads (F-0511/F-0531).
func RawJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// NormalizeBillID fails closed on a blank vendor bill id: a bill row without
// its original id cannot be deduped or traced.
func NormalizeBillID(id string) string {
	return strings.TrimSpace(id)
}
