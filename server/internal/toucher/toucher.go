// Package toucher 例行触达调度器（触达批 TOUCH-4，用户触达设计 §5）：月账单
// （自然月一轮，账单类必收）与入口域名例行清单（可配周期，可退订）两类例行
// 邮件。任务先落 touch_jobs 再经 herald.Emit 投递（outbox 语义，ferry 不自建
// 通道）；投出即标 sent 并记 event_id，通道分发失败由 TOUCH-5 回执改写。
package toucher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"gorm.io/gorm"
)

// settings 键：账单月闸门（同月不重发）与域名例行上次发送时刻。
const (
	settingBillMonth = "touch_bill_month"
	settingDomainsAt = "touch_domains_at"
)

// Config 是调度器参数：DomainsDays=域名例行周期（天，0=关），BaseURL=渲染
// 续费/订阅链接用面板对外地址。
type Config struct {
	DomainsDays int
	BaseURL     string
}

// Loop 周期调度直到 ctx 取消（对齐 notifyscan/recovery 的 Loop 口径）。
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

// Sweep 跑一轮：账单与域名例行各自独立，互不阻断，错误合并返回。
func Sweep(db *gorm.DB, cfg Config, now time.Time) error {
	return joinErr(sweepBill(db, cfg, now), sweepDomains(db, cfg, now))
}

func joinErr(a, b error) error {
	if a != nil {
		return fmt.Errorf("%v; %w", a, b)
	}
	return b
}

// sweepBill 月账单（§5 必收）：每自然月至多一轮（settings 月闸门），给全部
// 启用用户各落一单任务；内容=用量/配额/状态/到期/续费入口/已省下亮点。
func sweepBill(db *gorm.DB, cfg Config, now time.Time) error {
	month := now.UTC().Format("2006-01")
	if v, ok, err := storage.GetSetting(db, settingBillMonth); err != nil {
		return err
	} else if ok && v == month {
		return nil
	}
	var users []storage.User
	if err := db.Where("enabled = ?", true).Find(&users).Error; err != nil {
		return err
	}
	monthStart := month + "-01"
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

// emitBill 渲染并发一单月账单：任务落 touch_jobs，经 Emit 投出后标 sent。
func emitBill(db *gorm.DB, cfg Config, u storage.User, sv [3]int64, now time.Time) error {
	var usage struct {
		Rx, Tx int64
	}
	if err := db.Table("traffic_logs").
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Where("user_id = ? AND recorded_at >= ?", u.ID, now.UTC().Format("2006-01")+"-01").
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
	title := "月账单 · " + now.UTC().Format("2006-01")
	return emitTouch(db, int64(u.ID), herald.KindBill, title, body, now)
}

// sweepDomains 入口域名例行清单（§5 可退订）：距上次发送超 DomainsDays 天
// 给绑了邮箱且未退订例行的用户各落一单；0=关。
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

// emitDomains 渲染并发一单域名例行：主/备分组 + 公告订阅地址（断联容灾）。
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

// emitTouch 落任务并投递：touch_jobs 先行（任务不丢），Emit 成功记 event_id
// 并标 sent（ferry→Herald 腿 2xx 即视为投出）；Emit 失败任务留在 pending。
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

// formatBytes 十进制口径（与成本/节省报表一致）：GB=1e9。
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

// quotaText 配额展示：0=不限量。
func quotaText(quota int64) string {
	if quota <= 0 {
		return "不限量"
	}
	return formatBytes(quota)
}

// expireText 到期展示：nil=不限期。
func expireText(t *time.Time) string {
	if t == nil {
		return "不限期"
	}
	return t.UTC().Format("2006-01-02")
}
