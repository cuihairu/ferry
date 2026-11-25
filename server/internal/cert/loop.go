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
	"github.com/cuihairu/ferry/server/internal/safestr"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Loop sweeps certificate tasks periodically until ctx is canceled: pending, expiring soon (ok and inside the
// renewal window), and failed at its backoff point (failed and FailBackoff elapsed since the last attempt) are
// each driven in turn. store is the secret entry point (for dns-01 credential decryption; the master key comes via wiring injection).
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

// Sweep runs one orchestration round: issuance/renewal run serially inside the table sweep (few tasks, acme.sh
// takes seconds to minutes per call); a state flip to failed sends a notice, never repeated within one flip.
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

// dueForRetry is the failure backoff: retry only once FailBackoff has elapsed since the last attempt,
// and stop retrying once the attempt ceiling is reached (the task waits for a manual re-issue).
func dueForRetry(task storage.CertTask, now time.Time) bool {
	if task.Attempts >= MaxAttempts {
		return false
	}
	return task.LastAttempt == nil || now.Sub(*task.LastAttempt) >= FailBackoff
}

// runIssue drives one issuance: dns-01 looks up the DNS provider credential on the fly (BR-2 channel) and decrypts it
// into the environment; success backfills expiry and ok, failure records failed and sends a notice on the state flip.
func runIssue(ctx context.Context, db *gorm.DB, m *Manager, store *secret.Store, n *notify.Notifier, task storage.CertTask, now time.Time, logger *log.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, IssueTimeout)
	defer cancel()
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
		// The acme.sh cloudflare plugin expects CF_Token (API Token model).
		env["CF_Token"] = token
	}
	out, err := m.Issue(ctx, domains(task), task.Method, providerType, env)
	if err != nil {
		logger.Printf("cert issue task=%d %s: %s", task.ID, task.Domain, tail(out, 200))
		return finishIssue(db, task, now, StateFailed, tail(out, 400), n, logger)
	}
	logger.Printf("cert issued: task=%d %s", task.ID, task.Domain)
	// Do not roll back an issuance when the expiry cannot be read: keep the empty value, the next round's renewal window check skips it.
	if notAfter, err := m.Expiry(task.Domain); err == nil {
		task.NotAfter = &notAfter
	} else {
		logger.Printf("cert expiry read: task=%d %v", task.ID, err)
	}
	return finishIssue(db, task, now, StateOK, "", n, logger)
}

// finishIssue backfills the task terminal state; a flip to failed sends one notice, and a failure
// that reaches the attempt ceiling sends an extra exhausted notice (admin must re-issue manually).
func finishIssue(db *gorm.DB, task storage.CertTask, now time.Time, state, lastErr string, n *notify.Notifier, logger *log.Logger) error {
	flip := state == StateFailed && task.State != StateFailed
	updates := map[string]any{"state": state, "last_error": lastErr, "last_attempt": now, "updated_at": now}
	if state == StateFailed {
		updates["attempts"] = task.Attempts + 1
	} else {
		updates["attempts"] = 0
	}
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
	if state == StateFailed && task.Attempts+1 >= MaxAttempts {
		logger.Printf("cert retries exhausted: task=%d %s (%d attempts)", task.ID, task.Domain, task.Attempts+1)
		sendAlert(n, "cert_issue_exhausted", task, fmt.Sprintf("已连续失败 %d 次，自动重试停止，需人工排查后重发", task.Attempts+1))
	}
	return nil
}

// runRenew drives one renewal: success refreshes the expiry, failure flips to failed and notifies
// (same attempt ceiling as issuance: past MaxAttempts the sweep stops retrying until a manual re-issue).
func runRenew(ctx context.Context, db *gorm.DB, m *Manager, n *notify.Notifier, task storage.CertTask, now time.Time, logger *log.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, IssueTimeout)
	defer cancel()
	out, err := m.Renew(ctx, task.Domain)
	updates := map[string]any{"last_attempt": now, "updated_at": now}
	if err != nil {
		logger.Printf("cert renew task=%d %s: %s", task.ID, task.Domain, tail(out, 200))
		updates["state"] = StateFailed
		updates["last_error"] = tail(out, 400)
		updates["attempts"] = task.Attempts + 1
		if err := db.Model(&storage.CertTask{}).Where("id = ?", task.ID).Updates(updates).Error; err != nil {
			return err
		}
		logger.Printf("cert renew failed: task=%d %s", task.ID, task.Domain)
		sendAlert(n, "cert_renew_failed", task, tail(out, 400))
		if task.Attempts+1 >= MaxAttempts {
			logger.Printf("cert retries exhausted: task=%d %s (%d attempts)", task.ID, task.Domain, task.Attempts+1)
			sendAlert(n, "cert_renew_exhausted", task, fmt.Sprintf("已连续失败 %d 次，自动重试停止，需人工排查后重发", task.Attempts+1))
		}
		return nil
	}
	updates["attempts"] = 0
	if notAfter, err := m.Expiry(task.Domain); err == nil {
		updates["not_after"] = notAfter
	} else {
		logger.Printf("cert expiry read: task=%d %v", task.ID, err)
	}
	return db.Model(&storage.CertTask{}).Where("id = ?", task.ID).Updates(updates).Error
}

// emitExpiring emits the expiring alert (HERALD-3): entering the renewal window drops an event into the outbox, one per
// domain per day (dedup_key carries the date, checked on the ferry side); a successful renewal leaves the window and stops
// naturally; failures only log and do not block the orchestration.
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

// domains expands the main domain plus additional domains (comma separated) into a -d sequence.
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

// tail takes the tail segment of the output as an error summary (full output lives in the execution log): the start is moved
// forward to a UTF-8 character boundary (failed issuance/renewal output can contain Chinese certificate paths; a hard byte cut would slice a rune in half).
func tail(s string, n int) string {
	return safestr.Tail(strings.TrimSpace(s), n)
}

// IssueNow manually triggers one issuance (admin side): set issuing to prevent duplicates, then run asynchronously;
// terminal state and expiry are backfilled into the task row as usual.
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
		// The request context dies with the HTTP response; the background issuance
		// needs its own bounded context (runIssue applies the per-attempt timeout).
		if err := runIssue(context.Background(), db, m, store, notify.FromDB(db), task, time.Now(), logger); err != nil {
			logger.Printf("cert issue now: task=%d %v", task.ID, err)
		}
	}()
	return nil
}
