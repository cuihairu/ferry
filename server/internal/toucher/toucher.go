// Package toucher is the routine outreach scheduler (outreach batch TOUCH-4, user outreach design §5):
// monthly bills (one round per calendar month, bills always sent) and the entry-domain routine list
// (configurable period, unsubscribe-able) are the two routine emails. Jobs land in touch_jobs first and
// are delivered via herald.Emit (outbox semantics; ferry does not build its own channel); marked sent with event_id on dispatch, channel failures rewritten by TOUCH-5 receipts.
package toucher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"gorm.io/gorm"
)

// settings keys: bill month gate (no resend within the same month) and the last domains-routine send time.
const (
	settingBillMonth = "touch_bill_month"
	settingDomainsAt = "touch_domains_at"
)

// Config holds scheduler parameters: DomainsDays = domains routine period (days, 0 = off), BaseURL =
// the panel's public address used to render renewal/subscription links.
type Config struct {
	DomainsDays int
	BaseURL     string
}

// Loop schedules periodically until ctx is cancelled (same Loop criteria as notifyscan/recovery).
func Loop(ctx context.Context, db *gorm.DB, cfg Config, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
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
			if err := Sweep(db, cfg, time.Now()); err != nil {
				logger.Printf("toucher: %v", err)
			}
		}
	}
}

// Sweep runs one round: bill and domains routines are independent, neither blocks the other; errors are merged.
func Sweep(db *gorm.DB, cfg Config, now time.Time) error {
	return joinErr(sweepBill(db, cfg, now), sweepDomains(db, cfg, now))
}

func joinErr(a, b error) error {
	if a != nil {
		return fmt.Errorf("%v; %w", a, b)
	}
	return b
}

// sweepBill sends monthly bills (§5 always sent): at most one round per calendar month (settings month gate),
// one job per enabled user; content = usage/quota/state/expiry/renewal entry/savings highlights.
// The month gate follows the server's local calendar month (P2 local-timezone billing fix: keyed on the
// UTC month before, which misaligned bill months with the local calendar/traffic verdict window in non-UTC
// deployments). Highlights still take the UTC month start — save_stats aggregates by UTC day, so the pipeline stays self-consistent.
func sweepBill(db *gorm.DB, cfg Config, now time.Time) error {
	month := now.Format("2006-01")
	if v, ok, err := storage.GetSetting(db, settingBillMonth); err != nil {
		return err
	} else if ok && v == month {
		return nil
	}
	var users []storage.User
	if err := db.Where("enabled = ?", true).Find(&users).Error; err != nil {
		return err
	}
	monthStart := now.UTC().Format("2006-01") + "-01"
	savings := map[uint][3]int64{}
	for _, u := range users {
		d, c, b, err := save.UserSavings(db, u.ID, monthStart)
		if err != nil {
			return err
		}
		savings[u.ID] = [3]int64{d, c, b}
	}
	var err error
	for _, u := range users {
		if e := emitBill(db, cfg, u, savings[u.ID], now); e != nil && err == nil {
			err = e
		}
	}
	if err != nil {
		return err
	}
	return storage.SetSetting(db, settingBillMonth, month)
}

// emitBill renders and sends one monthly bill: the job lands in touch_jobs and is marked sent after Emit.
// Usage window = billing window: the server's local calendar month start (quota month criteria; P2 fix —
// it was keyed on the UTC string month start, which misaligned with the monthly traffic verdict / subscription display window in non-UTC deployments).
func emitBill(db *gorm.DB, cfg Config, u storage.User, sv [3]int64, now time.Time) error {
	var usage struct {
		Rx, Tx int64
	}
	if err := db.Table("traffic_logs").
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Where("user_id = ? AND recorded_at >= ?", u.ID, quota.WindowStart(quota.CycleMonth, now)).
		Scan(&usage).Error; err != nil {
		return err
	}
	used := usage.Rx + usage.Tx
	state := "正常"
	if !sub.UserActive(&u, used, now) {
		state = "不可用（已到期或超配额）"
	}
	body := fmt.Sprintf("用量：%s（自然月至今）\n配额：%s\n状态：%s\n到期：%s\n续费入口：%s/panel\n已为你省下：直连分流 %s + 广告拦截 %s（按节点用量占比折算）",
		formatBytes(used), quotaText(u.QuotaBytes), state, expireText(u.ExpiresAt), cfg.BaseURL,
		formatBytes(sv[0]), formatBytes(sv[2]))
	title := "月账单 · " + now.Format("2006-01")
	return emitTouch(db, int64(u.ID), herald.KindBill, title, body, now)
}

// sweepDomains sends the entry-domain routine list (§5 unsubscribe-able): past DomainsDays since the last
// send, one job per user with a bound email who has not unsubscribed from routines; 0 = off.
func sweepDomains(db *gorm.DB, cfg Config, now time.Time) error {
	if cfg.DomainsDays <= 0 {
		return nil
	}
	var lastStr string
	lastSet := false
	if v, ok, err := storage.GetSetting(db, settingDomainsAt); err != nil {
		return err
	} else if ok {
		lastStr, lastSet = v, true
	}
	if lastSet {
		last, err := time.Parse(time.RFC3339, lastStr)
		if err != nil {
			return err
		}
		if now.Sub(last) < time.Duration(cfg.DomainsDays)*24*time.Hour {
			return nil
		}
	}
	var domains []storage.EntryDomain
	if err := db.Where("enabled = ?", true).Order("id").Find(&domains).Error; err != nil {
		return err
	}
	var contacts []storage.UserContact
	if err := db.Where("email != '' AND routine_emails = ?", true).Find(&contacts).Error; err != nil {
		return err
	}
	var firstErr error
	for _, ct := range contacts {
		if e := emitDomains(db, cfg, ct, domains, now); e != nil && firstErr == nil {
			firstErr = e
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return storage.SetSetting(db, settingDomainsAt, now.Format(time.RFC3339))
}

// emitDomains renders and sends one domains routine: primary/backup grouping + the announcement feed address (outage resilience).
func emitDomains(db *gorm.DB, cfg Config, ct storage.UserContact, domains []storage.EntryDomain, now time.Time) error {
	var primary, backup string
	for _, d := range domains {
		if d.Role == "primary" && primary == "" {
			primary = d.Domain
			continue
		}
		backup += d.Domain + "\n"
	}
	body := "当前可用入口域名清单：\n"
	if primary != "" {
		body += "主入口：" + primary + "\n"
	}
	if backup != "" {
		body += "备用地址：\n" + backup
	}
	body += "公告订阅：" + cfg.BaseURL + "/feed.xml\n（域名变更会即时推邮件；请收藏本清单防失联）"
	return emitTouch(db, int64(ct.UserID), herald.KindDomains, "入口域名清单", body, now)
}

// emitTouch stores and delivers the job: touch_jobs first (no job loss); on Emit success the event_id
// is recorded and it is marked sent (a 2xx on the ferry→Herald leg counts as dispatched); on Emit failure the job stays pending.
func emitTouch(db *gorm.DB, userID int64, kind, title, body string, now time.Time) error {
	payload, err := json.Marshal(map[string]string{"title": title, "body": body})
	if err != nil {
		return err
	}
	job := storage.TouchJob{
		UserID: userID, Channel: "email", Kind: kind,
		Payload: string(payload), Status: herald.StatusPending, CreatedAt: now,
	}
	if err := db.Create(&job).Error; err != nil {
		return err
	}
	ev, err := herald.Emit(db, herald.EmitInput{
		Kind: kind, Severity: herald.SeverityInfo,
		Title: title, Body: body,
		Target: herald.TargetUser(userID),
		Meta:   map[string]any{"touch_job_id": job.ID},
	})
	if err != nil {
		job.Error = err.Error()
		return firstErr(db.Model(&job).Updates(map[string]any{"error": job.Error}).Error, err)
	}
	return db.Model(&job).Updates(map[string]any{
		"status": herald.StatusSent, "event_id": ev.ID, "sent_at": now,
	}).Error
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// formatBytes uses decimal units (consistent with the cost/savings reports): GB = 1e9.
func formatBytes(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1f KB", float64(n)/1e3)
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// quotaText renders the quota: 0 = unlimited.
func quotaText(quota int64) string {
	if quota <= 0 {
		return "不限量"
	}
	return formatBytes(quota)
}

// expireText renders the expiry: nil = no expiry; by the server's local calendar date (same zone as the
// billing window, so a non-UTC deployment does not show the expiry off by a day). Convert to local then
// take the calendar date — the timezone read back from the DB is not fixed (sqlite reads back UTC text), so
// a bare Format would drift with the read-back timezone (codex deep review P2).
func expireText(t *time.Time) string {
	if t == nil {
		return "不限期"
	}
	return t.Local().Format("2006-01-02")
}
