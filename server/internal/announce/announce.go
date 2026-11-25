// Package announce: announcement fan-out and windows (P2-5). Admin publishing and the periodic scan share one fan-out——
// before starts_at only a site-level anchor row is written (scheduled publish); once due, notifyscan fans out periodically;
// at ends_at the announcement is withdrawn (filtered from RSS and panel lists; the row is kept for queries); the external-channel
// resend slot backfills one round of notice events for already published announcements (delivering off-site only, no duplicate in-site message rows), rate-limited to 6h per announcement.
package announce

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// resendCooldown is the rate-limit window for external-channel resends: two resends of the same announcement are at least 6h apart.
const resendCooldown = 6 * time.Hour

// The two rejection reasons for resends (handlers map them to status codes).
var (
	// ErrCooldown duplicate resend of the same announcement inside the rate-limit window.
	ErrCooldown = errors.New("announcement resend cooldown")
	// ErrWindowClosed the announcement window has ended (withdrawal semantics win; no more external delivery).
	ErrWindowClosed = errors.New("announcement window closed")
	// ErrBadAudience invalid audience string (mapped to 400 on the API side).
	ErrBadAudience = errors.New("audience must be all or expiring:N (1-90 days)")
)

// Input is the payload for publishing one announcement.
type Input struct {
	Title    string
	Body     string
	StartsAt *time.Time // scheduled publish; nil=immediately
	EndsAt   *time.Time // scheduled withdrawal; nil=never withdraw
	Audience string     // audience: "all" (default) / "expiring:N" (expiring within N days)
}

// NormalizeAudience normalizes the audience string (strict validation at the publish entry): ""/"all" -> "all";
// "expiring:N" (N an integer 1-90) -> a canonically decimal "expiring:N"; anything else is rejected.
func NormalizeAudience(s string) (string, error) {
	if s == "" || s == "all" {
		return "all", nil
	}
	if rest, ok := strings.CutPrefix(s, "expiring:"); ok {
		if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= 90 {
			return "expiring:" + strconv.Itoa(n), nil
		}
	}
	return "", ErrBadAudience
}

// Fanout publishes one announcement: starts_at in the future = scheduled publish (only the anchor row is written, returns 0,
// fanned out by RunDue when due); otherwise fan out immediately (anchor row + enabled-user in-site message rows + notice event
// external delivery). Returns the number of users actually reached (disabled users receive nothing).
func Fanout(db *gorm.DB, in Input) (int64, error) {
	now := time.Now()
	aud, err := NormalizeAudience(in.Audience)
	if err != nil {
		return 0, err
	}
	anchor := storage.Notification{
		Type: storage.NotifAnnouncement, Title: in.Title, Body: in.Body,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Audience: aud, CreatedAt: now,
	}
	if in.StartsAt != nil && in.StartsAt.After(now) {
		if err := db.Create(&anchor).Error; err != nil {
			return 0, err
		}
		return 0, nil
	}
	return fanoutUsers(db, &anchor)
}

// RunDue fans out due scheduled announcements (called periodically by notifyscan): the anchor row's starts_at has passed
// and it has not been fanned out yet. Returns the number of newly reached users this round.
func RunDue(db *gorm.DB, now time.Time) (int, error) {
	var due []storage.Notification
	if err := db.Where("user_id = ? AND type = ? AND fanout_at IS NULL AND starts_at IS NOT NULL AND starts_at <= ?",
		0, storage.NotifAnnouncement, now).Order("id").Find(&due).Error; err != nil {
		return 0, err
	}
	n := 0
	for i := range due {
		cnt, err := fanoutUsers(db, &due[i])
		if err != nil {
			return n, err
		}
		n += int(cnt)
	}
	return n, nil
}

// Resend external-channel resend (P2-5 (3)): for an already published announcement, write another round of notice events for
// all enabled users (Herald delivers TG/email)——in-site message rows are not duplicated (users already have them).
// Rate limit: at most once per announcement inside resendCooldown; ended announcements are not resent. Returns users delivered to this round.
func Resend(db *gorm.DB, anchorID int64, now time.Time) (int64, error) {
	var anchor storage.Notification
	if err := db.Where("id = ? AND user_id = ? AND type = ?", anchorID, 0, storage.NotifAnnouncement).
		First(&anchor).Error; err != nil {
		return 0, err
	}
	if anchor.EndsAt != nil && !anchor.EndsAt.After(now) {
		return 0, ErrWindowClosed
	}
	if anchor.ResendAt != nil && now.Sub(*anchor.ResendAt) < resendCooldown {
		return 0, ErrCooldown
	}
	var ids []int64
	if err := audienceScope(db, &anchor, now).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	events := make([]storage.Event, 0, len(ids))
	for _, id := range ids {
		events = append(events, storage.Event{
			Kind: herald.KindNotice, Severity: herald.SeverityInfo,
			Title: anchor.Title, Body: anchor.Body,
			Target: herald.TargetUser(id), Status: herald.StatusPending,
			OccurredAt: now, CreatedAt: now,
			DedupKey: resendDedupKey(anchor.ID, now),
		})
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(events, 500).Error; err != nil {
			return 0, err
		}
	}
	if err := db.Model(&anchor).Update("resend_at", now).Error; err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// resendDedupKey lets resends of the same announcement in the same round merge on the Herald side (rounds floored to minutes).
func resendDedupKey(anchorID int64, now time.Time) string {
	return "notice_resend:" + strconv.FormatInt(anchorID, 10) + ":" + now.Format("200601021504")
}

// audienceScope filters enabled users by the anchor row's audience (fan-out and resend share the roster semantics):
// all=all enabled users; expiring:N=enabled users whose expires_at falls in (now, now+N days]
// (the expiring roster is fixed at fan-out time——scheduled announcements are parsed only when due; resends recompute at resend time).
// Invalid audience strings fall back to all (dirty data must not block fan-out; the publish entry already validates strictly).
func audienceScope(db *gorm.DB, anchor *storage.Notification, now time.Time) *gorm.DB {
	q := db.Model(&storage.User{}).Where("enabled = ?", true)
	if rest, ok := strings.CutPrefix(anchor.Audience, "expiring:"); ok {
		if n, err := strconv.Atoi(rest); err == nil && n >= 1 && n <= 90 {
			q = q.Where("expires_at IS NOT NULL AND expires_at > ? AND expires_at <= ?", now, now.AddDate(0, 0, n))
		}
	}
	return q
}

// fanoutUsers writes in-site message rows for enabled users matched by the audience and delivers notice events externally;
// the anchor row's ID is written first (user rows link via AnchorID; sent n / read m statistics both count by it). Afterwards it writes back
// FanoutAt.
func fanoutUsers(db *gorm.DB, anchor *storage.Notification) (int64, error) {
	now := time.Now()
	var ids []int64
	if err := audienceScope(db, anchor, now).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if anchor.CreatedAt.IsZero() {
		anchor.CreatedAt = now
	}
	// On the RunDue path the anchor row already exists (written when scheduled), so only the create path inserts it.
	if anchor.ID == 0 {
		if err := db.Create(anchor).Error; err != nil {
			return 0, err
		}
	}
	rows := make([]storage.Notification, 0, len(ids))
	events := make([]storage.Event, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, storage.Notification{
			UserID: id, Type: storage.NotifAnnouncement,
			Title: anchor.Title, Body: anchor.Body,
			AnchorID: anchor.ID, EndsAt: anchor.EndsAt,
			CreatedAt: now,
		})
		events = append(events, storage.Event{
			Kind: herald.KindNotice, Severity: herald.SeverityInfo,
			Title: anchor.Title, Body: anchor.Body,
			Target: herald.TargetUser(id), Status: herald.StatusPending,
			OccurredAt: now, CreatedAt: now,
		})
	}
	if err := db.CreateInBatches(rows, 500).Error; err != nil {
		return 0, err
	}
	if len(events) > 0 {
		if err := db.CreateInBatches(events, 500).Error; err != nil {
			return int64(len(ids)), err
		}
	}
	if err := db.Model(anchor).Update("fanout_at", now).Error; err != nil {
		return int64(len(ids)), err
	}
	// Returns the user-row count (the anchor row is not counted): fan-out semantics = how many enabled users were reached.
	return int64(len(ids)), nil
}
