// Package herald is the event outbox ("alert channel design" HERALD-1): ferry builds no delivery
// channel of its own - events (admin alerts + user outreach) all land in the local outbox and are delivered to Herald
// asynchronously, with channel dispatch (TG/WeChat/email) managed by Herald. When the panel is offline or Herald
// is unreachable, events are not lost (pending retries, marked red as failed dead-letters past the limit), and are
package herald

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Event status and severity values.
const (
	StatusPending = "pending" // awaiting delivery / retrying
	StatusSent    = "sent"    // delivered (Herald accepted)
	StatusFailed  = "failed"  // dead-lettered past the retry limit (dash flags in red, channel failures are never silently dropped)

	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// Delivery-leg channel name: the ferry→Herald POST result lands in event_deliveries under this;
// Herald's async channel-dispatch receipts (HERALD-2) use the concrete channel name.
const ChannelHerald = "herald"

// Event kinds and target values ("alert channel design" §2 list; wired in per class from HERALD-3 on).
const (
	KindRegionFault     = "region_fault"       // region aggregate fully faulty
	KindISPFault        = "isp_fault"          // ISP aggregate fully faulty
	KindNodeBlocked     = "node_blocked"       // node judged banned, entered the recovery pipeline
	KindNodeDown        = "node_down"          // single-point auto removal
	KindCostExceeded    = "cost_exceeded"      // high-cost alert activated
	KindPayRecon        = "payrecon_flag"      // payment reconciliation faults found by the scheduled task (F-1308, deduped per day fleet-wide)
	KindProcCrashed     = "proc_crashed"       // process restart after crash failed
	KindRecoveryFailed  = "recovery_failed"    // recovery pipeline exhausted all tiers (escalates to a human)
	KindCertExpiring    = "cert_expiring"      // certificate nearing expiry
	KindBackupFailed    = "backup_failed"      // backup failure (panel availability §2.2, P1 periodic backup wired to production points)
	KindLoginAlert      = "login_alert"        // anomalous admin login (security design §1: consecutive failures / new subnet, login audit wired to production points)
	KindPriceAlert      = "price_alert"        // price watch hit a drop / target price (plans and cost design §4.2, E-32 reference-library batch wired to production points)
	KindTaskBacklog     = "task_backlog"       // unified task queue backlog over threshold (F-1615)
	KindTaskDead        = "task_dead"          // unified task dead-letter accumulation over threshold (F-1615)
	KindGeoSyncFailed   = "geodns_sync_failed" // smart-DNS per-region reconciliation failure (entries and load balancing §C.8, E-27 wired to production points)
	KindDeviceLimit     = "device_limit"       // subscription client device/IP count exceeded (P2-4, deduped per user per day)
	KindDeviceAnomaly   = "device_anomaly"     // abnormal subscription device behavior, e.g. share-style churn (F-1226, deduped per user per day)
	KindTrafficAnomaly  = "traffic_anomaly"    // user traffic spike vs trailing baseline (F-1252, deduped per user per day)
	KindCloudBootFailed = "cloud_boot_failed"  // cloud instance booted but its Ferry Node did not come back (F-0403)

	// User outreach (HERALD-4, §2.2; in-app messages still go out, this channel serves out-of-app delivery).
	KindExpire = "expire" // account expiry reminder
	KindQuota  = "quota"  // traffic warning
	KindOrder  = "order"  // grant / order result
	KindNotice = "notice" // announcement

	// Routine outreach (TOUCH-4, §5): bill types are mandatory by default, the domain routine one can be unsubscribed.
	KindBill    = "bill"    // monthly bill (usage/status/expiry/renewal entry/highlight of savings)
	KindDomains = "domains" // routine entry-domain list (kept fresh, unsubscribable)
	KindContact = "contact" // escalation after consecutive delivery failures to the contact (retry on another channel, TOUCH-5)

	TargetAdmin = "admin"
)

// TargetUser target values for the user dimension (user:<id>).
func TargetUser(id int64) string {
	return fmt.Sprintf("user:%d", id)
}

// ValidSeverity reports whether a severity value is legal.
func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// EmitInput is the payload on the event-producing side (from HERALD-3 on, the nine admin alert classes and,
// from the outreach batch on, user events all go through here). Meta is any JSON-serializable object, persisted as JSON text.
type EmitInput struct {
	Kind       string
	Severity   string
	Title      string
	Body       string
	Target     string // admin / user:<id>
	DedupKey   string // dedup-key fallback (same-key merging within the window is Herald's job; ferry only passes it through)
	Meta       any
	OccurredAt time.Time
}

// Emit lands one pending event and returns immediately (outbox semantics: the producing side never touches the network).
func Emit(db *gorm.DB, in EmitInput) (*storage.Event, error) {
	if !ValidSeverity(in.Severity) {
		in.Severity = SeverityInfo
	}
	if in.OccurredAt.IsZero() {
		in.OccurredAt = time.Now()
	}
	now := time.Now()
	meta := ""
	if in.Meta != nil {
		if raw, err := json.Marshal(in.Meta); err == nil {
			meta = string(raw)
		}
	}
	row := storage.Event{
		Kind: in.Kind, Severity: in.Severity,
		Title: in.Title, Body: in.Body,
		Target: in.Target, DedupKey: in.DedupKey, Meta: meta,
		Status: StatusPending, OccurredAt: in.OccurredAt, CreatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Count returns per-status event counts (dash red-flag policy: failed>0 means an undigested channel failure).
func Count(db *gorm.DB) (pending, sent, failed int64, err error) {
	type row struct {
		Status string
		N      int64
	}
	var rows []row
	if err = db.Model(&storage.Event{}).Select("status, COUNT(*) AS n").Group("status").Scan(&rows).Error; err != nil {
		return
	}
	for _, r := range rows {
		switch r.Status {
		case StatusPending:
			pending = r.N
		case StatusSent:
			sent = r.N
		case StatusFailed:
			failed = r.N
		}
	}
	return
}
