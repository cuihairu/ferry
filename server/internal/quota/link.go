// Quota linkage for the quota package (SAVE-6, "Traffic Saving Design" §5): when a user's traffic or cost exceeds
// a threshold, the subscription is downgraded automatically and its entries narrow to the low-cost tier (monthly =
// zero marginal cost, or metered nodes priced within the tier line), so new connections land there while existing
// connections stay put. No throttling action: nodes share credentials with no per-user identity, so shaping has
// nothing to attach to (P2-3); the only linkage action is "subscription downgrade", with trigger and release trailed (quota_actions).
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// settingKey is the persistence key of the linkage setting (panel KV).
const settingKey = "quota_link"

// Trigger values (QuotaAction.Trigger).
const (
	LinkTriggerTraffic = "traffic" // traffic tier: window usage reaches a percentage of the personal quota
	LinkTriggerCost    = "cost"    // cost tier: window converted cost reaches an absolute threshold
)

// Default tier: traffic at 90% (leaves a buffer after the 80% warning before downgrading).
const DefaultTrafficPercent = 90

// LinkSetting is the linkage setting (dash-editable, off by default). Cannot be enabled when both tier triggers are off.
type LinkSetting struct {
	Enabled bool `json:"enabled"`
	// TrafficPercent is the traffic-tier threshold (1-100): triggers when window usage reaches N% of the personal quota;
	// 0 = no traffic-tier trigger. Only applies to users with quota > 0.
	TrafficPercent int `json:"traffic_percent"`
	// CostCents is the cost-tier threshold (cents): triggers when the window converted cost (per-log metered nodes times unit price)
	// reaches N cents; 0 = no cost-tier trigger. The only trigger tier for users with quota 0 (unlimited).
	CostCents int64 `json:"cost_cents"`
	// MaxPriceCents is the downgrade cost tier line (cents/GB): monthly (zero marginal cost) or metered nodes
	// priced within the line count as the low-cost tier. 0 = only monthly nodes count as the low-cost tier.
	MaxPriceCents int64 `json:"max_price_cents"`
}

// DefaultLinkSetting is the default setting: off. When enabled, the traffic tier starts at 90%.
func DefaultLinkSetting() LinkSetting {
	return LinkSetting{Enabled: false, TrafficPercent: DefaultTrafficPercent}
}

// LoadLinkSetting reads the linkage setting; unset falls back to the default, corrupt data follows the default without blocking.
func LoadLinkSetting(db *gorm.DB) (LinkSetting, error) {
	setting := DefaultLinkSetting()
	raw, ok, err := storage.GetSetting(db, settingKey)
	if err != nil || !ok {
		return setting, err
	}
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		return DefaultLinkSetting(), nil
	}
	setting.normalize()
	return setting, nil
}

// normalize pulls out-of-range fields back in line: negative or out-of-bounds thresholds follow the default/off.
func (s *LinkSetting) normalize() {
	if s.TrafficPercent < 0 || s.TrafficPercent > 100 {
		s.TrafficPercent = DefaultTrafficPercent
	}
	if s.CostCents < 0 {
		s.CostCents = 0
	}
	if s.MaxPriceCents < 0 {
		s.MaxPriceCents = 0
	}
}

// SaveLinkSetting validates and persists the linkage setting: when enabled, at least one tier must have a trigger threshold,
// otherwise the linkage is a no-op while still occupying the "enabled" semantics, so reject outright.
func SaveLinkSetting(db *gorm.DB, setting LinkSetting) error {
	setting.normalize()
	if setting.Enabled && setting.TrafficPercent == 0 && setting.CostCents == 0 {
		return errors.New("enabled 联动至少要配置一个触发阈值（traffic_percent 或 cost_cents）")
	}
	raw, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	return storage.SetSetting(db, settingKey, string(raw))
}

// Trigger decides whether the user triggers the linkage and returns the matched trigger (empty string when none
// matches, traffic tier first). used is the window usage, costCents the window converted cost.
func (s LinkSetting) Trigger(used, quotaBytes, costCents int64) string {
	if !s.Enabled {
		return ""
	}
	if s.TrafficPercent > 0 && quotaBytes > 0 && used*100 >= quotaBytes*int64(s.TrafficPercent) {
		return LinkTriggerTraffic
	}
	if s.CostCents > 0 && costCents >= s.CostCents {
		return LinkTriggerCost
	}
	return ""
}

// LowCost reports whether a node belongs to the low-cost tier (visible to a downgraded subscription): monthly/fixed bandwidth
// have zero marginal cost, and metered nodes must be priced within the tier line. Aligned with alloc.Candidate.CostPerGB —
// only metered billing carries marginal cost.
func (s LinkSetting) LowCost(n storage.Node) bool {
	if n.BillingType != "按流量" {
		return true
	}
	return n.TrafficPriceCents <= s.MaxPriceCents
}

// ActiveAction returns the user's effective linkage downgrade row (subscription egress filters by its snapshot tier line);
// returns nil when no row is effective.
func ActiveAction(db *gorm.DB, userID uint) (*storage.QuotaAction, error) {
	var row storage.QuotaAction
	err := db.Where("user_id = ? AND released_at IS NULL", userID).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// WindowCostCents converts the user's window traffic cost (cents): each accounting entry times the unit price (cents/GB) of its
// metered node, then integer-divided per GB and summed; monthly/fixed bandwidth with zero marginal cost is not counted (aligned
// with the alloc cost model). The unit price multiplies bytes and is divided by 2^30 at the end, rounding small-traffic remainders down.
func WindowCostCents(db *gorm.DB, userID uint, cycle string, now time.Time) (int64, error) {
	q := db.Model(&storage.TrafficLog{}).
		Select("COALESCE(SUM((traffic_logs.rx_bytes + traffic_logs.tx_bytes) * nodes.traffic_price_cents), 0)").
		Joins("LEFT JOIN nodes ON nodes.id = traffic_logs.node_id").
		Where("traffic_logs.user_id = ? AND nodes.billing_type = ?", userID, "按流量")
	if since := WindowStart(cycle, now); !since.IsZero() {
		q = q.Where("traffic_logs.recorded_at >= ?", since)
	}
	var raw int64
	if err := q.Scan(&raw).Error; err != nil {
		return 0, err
	}
	return raw / (1 << 30), nil
}

// Sweep runs one round of linkage evaluation: a trigger creates a downgrade trail and sends an in-app message (an already
// effective row is neither re-created nor re-notified); a user who no longer triggers (usage recedes/window rolls/
// threshold relaxed) or a disabled linkage releases the trail. Returns the number of downgrade rows created this round.
func Sweep(db *gorm.DB, now time.Time) (int, error) {
	setting, err := LoadLinkSetting(db)
	if err != nil {
		return 0, err
	}
	if !setting.Enabled {
		return 0, releaseAll(db, now, "联动关闭")
	}
	var users []storage.User
	if err := db.Where("enabled = ?", true).Find(&users).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, u := range users {
		active, err := ActiveAction(db, u.ID)
		if err != nil {
			return created, err
		}
		used, err := UsedBytes(db, u.ID, u.ResetCycle, now)
		if err != nil {
			return created, err
		}
		cost, err := WindowCostCents(db, u.ID, u.ResetCycle, now)
		if err != nil {
			return created, err
		}
		trigger := setting.Trigger(used, u.QuotaBytes, cost)
		switch {
		case trigger != "" && active == nil:
			if err := apply(db, now, u, setting, trigger, used, cost); err != nil {
				return created, err
			}
			created++
		case trigger == "" && active != nil:
			if err := db.Model(&storage.QuotaAction{}).Where("id = ?", active.ID).
				Updates(map[string]any{"released_at": now, "release_reason": "阈值回落"}).Error; err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

// apply records one downgrade trail and sends an in-app message. reason carries the trigger model (percent/amount),
// aligned with the landing_assignments.reason trail convention.
func apply(db *gorm.DB, now time.Time, u storage.User, setting LinkSetting, trigger string, used, cost int64) error {
	reason := fmt.Sprintf("费用达阈值 ¥%.2f", float64(cost)/100)
	if trigger == LinkTriggerTraffic {
		reason = fmt.Sprintf("流量达配额 %d%%（%s/%s）", setting.TrafficPercent, humanMB(used), humanMB(u.QuotaBytes))
	}
	row := storage.QuotaAction{
		UserID: u.ID, Trigger: trigger,
		UsedBytes: used, QuotaBytes: u.QuotaBytes, CostCents: cost,
		MaxPriceCents: setting.MaxPriceCents,
		Reason:        reason, CreatedAt: now,
	}
	if err := db.Create(&row).Error; err != nil {
		return err
	}
	// In-app message (NT system type, delivered straight to the single user): tells about the downgrade and the recovery model.
	return db.Create(&storage.Notification{
		UserID: int64(u.ID), Type: storage.NotifSystem,
		Title:     "已达用量阈值，订阅已切换低成本线路",
		Body:      reason + "；用量回落后订阅将自动恢复完整节点列表。",
		CreatedAt: now,
	}).Error
}

// humanMB renders a human-readable magnitude of bytes (MB/GB rounded up), used only in trail text.
func humanMB(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%dGB", (b+(1<<30)-1)>>30)
	case b >= 1<<20:
		return fmt.Sprintf("%dMB", (b+(1<<20)-1)>>20)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

// releaseAll releases all effective downgrade rows (fallback cleanup when the linkage is turned off).
func releaseAll(db *gorm.DB, now time.Time, reason string) error {
	return db.Model(&storage.QuotaAction{}).Where("released_at IS NULL").
		Updates(map[string]any{"released_at": now, "release_reason": reason}).Error
}

// LinkLoop runs linkage evaluation periodically until ctx is canceled (aligned with alloc.Loop: one round on start;
// the linkage state takes effect at subscription egress immediately, the loop only handles state transitions and notices).
func LinkLoop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n, err := Sweep(db, time.Now()); err != nil {
			logger.Printf("quota link sweep: %v", err)
		} else if n > 0 {
			logger.Printf("quota link: %d user(s) downgraded", n)
		}
		t.Reset(interval)
	}
}
