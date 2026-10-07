package cert

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Loop 周期扫证书任务直到 ctx 取消：待签（pending）、临期（ok 且进入
// 续期窗口）、失败到退避点（failed 且距上次尝试满 FailBackoff）逐个驱动。
// store 是机密入口（dns-01 凭证解密用，master key 走装配注入）。
func Loop(ctx context.Context, db *gorm.DB, m *Manager, store *secret.Store, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := Sweep(ctx, db, m, store, time.Now(), logger); err != nil {
			logger.Printf("cert sweep: %v", err)
		}
		t.Reset(interval)
	}
}

// Sweep 执行一轮编排：签发/续期在扫表内串行执行（任务量小，acme.sh
// 单次秒到分钟级）；状态翻转失败发通知，翻转内不重复发。
func Sweep(ctx context.Context, db *gorm.DB, m *Manager, store *secret.Store, now time.Time, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	var tasks []storage.CertTask
	if err := db.Find(&tasks).Error; err != nil {
		return err
	}
	notifier := notify.FromDB(db)
	for _, task := range tasks {
		switch {
		case task.State == StatePending, task.State == StateFailed && dueForRetry(task, now):
			if err := runIssue(ctx, db, m, store, notifier, task, now, logger); err != nil {
				logger.Printf("cert issue: task=%d %v", task.ID, err)
			}
		case task.State == StateOK && task.NotAfter != nil && task.NotAfter.Before(now.Add(RenewBefore)):
			emitExpiring(db, task, now, logger)
			if err := runRenew(ctx, db, m, notifier, task, now, logger); err != nil {
				logger.Printf("cert renew: task=%d %v", task.ID, err)
			}
		}
	}
	return nil
}

// dueForRetry 失败退避：距上次尝试满 FailBackoff 才再试。
func dueForRetry(task storage.CertTask, now time.Time) bool {
	return task.LastAttempt == nil || now.Sub(*task.LastAttempt) >= FailBackoff
}

// runIssue 驱动一次签发：dns-01 现查 DNS 商凭证（BR-2 通道）解密进环境；
// 成功回填到期与 ok，失败记 failed 并在状态翻转时发通知。
func runIssue(ctx context.Context, db *gorm.DB, m *Manager, store *secret.Store, n *notify.Notifier, task storage.CertTask, now time.Time, logger *log.Logger) error {
	env := map[string]string{}
	providerType := ""
	if task.Method == MethodDNS01 {
		if task.DNSProviderID == 0 {
			return finishIssue(db, task, now, StateFailed, "dns provider not configured", n, logger)
		}
		var prov storage.DNSProvider
		if err := db.First(&prov, task.DNSProviderID).Error; err != nil {
			return finishIssue(db, task, now, StateFailed, "dns provider missing", n, logger)
		}
		if !prov.Enabled {
			return finishIssue(db, task, now, StateFailed, "dns provider disabled", n, logger)
		}
		if store == nil || !store.Enabled() {
			return finishIssue(db, task, now, StateFailed, "secret master key not configured (set FERRY_SECRET_KEY)", n, logger)
		}
		token, err := store.Decrypt(prov.APIKey)
		if err != nil {
			return finishIssue(db, task, now, StateFailed, "dns token decrypt failed", n, logger)
		}
		providerType = prov.Type
		// acme.sh 的 cloudflare 插件认 CF_Token（API Token 口径）。
		env["CF_Token"] = token
	}
	out, err := m.Issue(ctx, domains(task), task.Method, providerType, env)
	if err != nil {
		logger.Printf("cert issue task=%d %s: %s", task.ID, task.Domain, tail(out, 200))
		return finishIssue(db, task, now, StateFailed, tail(out, 400), n, logger)
	}
	logger.Printf("cert issued: task=%d %s", task.ID, task.Domain)
	// 到期读不出不回滚签发：保留空值，下轮续期窗口判定跳过。
	if notAfter, err := m.Expiry(task.Domain); err == nil {
		task.NotAfter = &notAfter
	} else {
		logger.Printf("cert expiry read: task=%d %v", task.ID, err)
	}
	return finishIssue(db, task, now, StateOK, "", n, logger)
}

// finishIssue 回填任务终态；翻转到 failed 发一次通知。
func finishIssue(db *gorm.DB, task storage.CertTask, now time.Time, state, lastErr string, n *notify.Notifier, logger *log.Logger) error {
	flip := state == StateFailed && task.State != StateFailed
	updates := map[string]any{"state": state, "last_error": lastErr, "last_attempt": now, "updated_at": now}
	if task.NotAfter != nil {
		updates["not_after"] = task.NotAfter
	}
	if err := db.Model(&storage.CertTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
		return err
	}
	if flip {
		logger.Printf("cert failed: task=%d %s", task.ID, task.Domain)
		sendAlert(n, "cert_issue_failed", task, lastErr)
	}
	return nil
}

// runRenew 驱动一次续期：成功刷新到期，失败翻 failed 并通知。
func runRenew(ctx context.Context, db *gorm.DB, m *Manager, n *notify.Notifier, task storage.CertTask, now time.Time, logger *log.Logger) error {
	out, err := m.Renew(ctx, task.Domain)
	updates := map[string]any{"last_attempt": now, "updated_at": now}
	if err != nil {
		logger.Printf("cert renew task=%d %s: %s", task.ID, task.Domain, tail(out, 200))
		updates["state"] = StateFailed
		updates["last_error"] = tail(out, 400)
		if err := db.Model(&storage.CertTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
			return err
		}
		logger.Printf("cert renew failed: task=%d %s", task.ID, task.Domain)
		sendAlert(n, "cert_renew_failed", task, tail(out, 400))
		return nil
	}
	if notAfter, err := m.Expiry(task.Domain); err == nil {
		updates["not_after"] = notAfter
	} else {
		logger.Printf("cert expiry read: task=%d %v", task.ID, err)
	}
	return db.Model(&storage.CertTask{}).Where("id = ?", task.ID).Updates(updates).Error
}

// emitExpiring 临期告警（HERALD-3）：进续期窗口即落事件 outbox，同域名
// 同日只发一条（dedup_key 带日，ferry 侧查重），续期成功窗口退出自然停发；
// 失败只记日志不阻断编排。
func emitExpiring(db *gorm.DB, task storage.CertTask, now time.Time, logger *log.Logger) {
	key := fmt.Sprintf("cert:%s:cert_expiring:%s", task.Domain, now.Format("20060102"))
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ? AND dedup_key = ?", herald.KindCertExpiring, key).Count(&n).Error; err != nil {
		logger.Printf("cert expiring dedup check: task=%d %v", task.ID, err)
		return
	}
	if n > 0 {
		return
	}
	days := int(math.Ceil(task.NotAfter.Sub(now).Hours() / 24))
	title := fmt.Sprintf("证书 %s 已到期，续期仍未完成", task.Domain)
	if days > 0 {
		title = fmt.Sprintf("证书 %s 将于 %d 天后到期", task.Domain, days)
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindCertExpiring, Severity: herald.SeverityWarning,
		Title:    title,
		Body:     fmt.Sprintf("到期时间 %s，已进入续期窗口，续期由证书编排自动驱动", task.NotAfter.Format("2006-01-02")),
		Target:   herald.TargetAdmin,
		DedupKey: key,
		Meta:     map[string]any{"task_id": task.ID, "domain": task.Domain, "not_after": task.NotAfter.Format(time.RFC3339)},
	}); err != nil {
		logger.Printf("herald emit cert_expiring: task=%d %v", task.ID, err)
	}
}

// domains 主域名 + 附加域名（逗号分隔）展开成 -d 序列。
func domains(task storage.CertTask) []string {
	out := []string{task.Domain}
	for _, s := range strings.Split(task.Sans, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sendAlert(n *notify.Notifier, event string, task storage.CertTask, detail string) {
	if n == nil || !n.Enabled() {
		return
	}
	_ = n.Send(notify.Event{
		Event: event,
		Text:  "证书任务失败：" + task.Domain + "（" + detail + "）",
		Fields: map[string]any{
			"task_id": task.ID, "domain": task.Domain, "method": task.Method,
		},
	})
}

// tail 取输出尾段作错误摘要（完整输出在执行日志）。
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// IssueNow 手动触发一次签发（管理端）：置 issuing 防重后异步执行，
// 终态与到期照常回填任务行。
func IssueNow(ctx context.Context, db *gorm.DB, m *Manager, store *secret.Store, taskID uint, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	var task storage.CertTask
	if err := db.First(&task, taskID).Error; err != nil {
		return err
	}
	if err := db.Model(&storage.CertTask{}).Where("id = ?", task.ID).
		Updates(map[string]any{"state": "issuing", "updated_at": time.Now()}).Error; err != nil {
		return err
	}
	go func() {
		if err := runIssue(ctx, db, m, store, notify.FromDB(db), task, time.Now(), logger); err != nil {
			logger.Printf("cert issue now: task=%d %v", task.ID, err)
		}
	}()
	return nil
}
