package handler

// Admin login audit (security design section 1, P1): every login attempt is
// written to login_logs (time/IP/UA/result, matching the design DDL) and is
// pageable in the dash; consecutive failures and successful logins from a new
// subnet raise a Herald login_alert -- with Herald unconfigured the event
// lands in the local outbox (visible on the dash event page and as an in-app
// message), never silenced and never routed through a self-built channel.

import (
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Consecutive-failure window and threshold, aligned with the login rate limit
// (5 failures within 15 minutes locks out).
const (
	loginBruteWindow = 15 * time.Minute
	loginBruteFailN  = 5
	loginLogUALimit  = 255
)

// writeLoginLog appends one login audit row; a failure only logs and never
// blocks the login response.
func (h *Handler) writeLoginLog(username, ip, ua string, ok bool) storage.LoginLog {
	// UA truncation cuts on character boundaries (a UA may carry non-ASCII names,
	// and a hard byte cut would split a multibyte char and store invalid UTF-8).
	ua = truncate(ua, loginLogUALimit)
	row := storage.LoginLog{Username: username, IP: ip, UA: ua, OK: ok, CreatedAt: time.Now()}
	if err := h.db.Create(&row).Error; err != nil {
		log.Printf("login log: %v", err)
	}
	return row
}

// alertLoginBrute fires the consecutive-failure alert: once failures from the
// same IP reach the threshold inside the window, emit login_alert. Dedup is
// by IP+day so repeated bursts on the same day do not spam.
func (h *Handler) alertLoginBrute(ip string) {
	var n int64
	if err := h.db.Model(&storage.LoginLog{}).
		Where("ip = ? AND ok = ? AND created_at > ?", ip, false, time.Now().Add(-loginBruteWindow)).
		Count(&n).Error; err != nil {
		return
	}
	if n < loginBruteFailN {
		return
	}
	emitLoginAlert(h.db, fmt.Sprintf("管理员登录连续失败（%s）", ip),
		fmt.Sprintf("IP %s 于 %s 内登录失败 %d 次，已达锁定阈值；非本人操作请立即改密并检查两步验证绑定",
			ip, loginBruteWindow, n),
		"brute:"+ip+":"+time.Now().Format("2006-01-02"))
}

// alertLoginNewIP fires the new-subnet success alert: a successful login from a
// subnet other than the account's last successful one (v4 /24, v6 /64
// granularity) raises it; the very first success has no baseline, so nothing is
// flagged. afterID is this success row's ID (exclude itself, take the
// previous one). Dedup is by user+day.
func (h *Handler) alertLoginNewIP(username, ip string, afterID uint) {
	var last storage.LoginLog
	if err := h.db.Where("username = ? AND ok = ? AND id < ?", username, true, afterID).
		Order("id DESC").First(&last).Error; err != nil {
		return
	}
	if sameNetwork(last.IP, ip) {
		return
	}
	emitLoginAlert(h.db, fmt.Sprintf("管理员新网段登录（%s）", username),
		fmt.Sprintf("账号 %s 于新网段登录成功：%s（上次成功 %s）；非本人操作请立即改密",
			username, ip, last.IP),
		"newip:"+username+":"+time.Now().Format("2006-01-02"))
}

// sameNetwork decides whether two IPs share a subnet ("remote" granularity:
// IPv4 /24, IPv6 /64); on a parse failure it falls back to comparing the
// whole strings.
func sameNetwork(a, b string) bool {
	ia, err1 := netip.ParseAddr(a)
	ib, err2 := netip.ParseAddr(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	ia, ib = ia.Unmap(), ib.Unmap()
	bits := 24
	if ia.Is6() {
		bits = 64
	}
	pa := netip.PrefixFrom(ia, bits).Masked()
	pb := netip.PrefixFrom(ib, bits).Masked()
	return pa.IsValid() && pb.IsValid() && pa == pb
}

// emitLoginAlert lands a login_alert event (warning/admin): dedup is checked
// locally by dedup_key (at most one per key per day -- Emit only passes
// through and does no local dedup), and the dedup_key also feeds Herald-side
// window merging; with Herald unconfigured the event lands in the local outbox
// and stays visible in-app.
func emitLoginAlert(db *gorm.DB, title, body, dedup string) {
	key := "login_alert:" + dedup
	var n int64
	if err := db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindLoginAlert, key).Count(&n).Error; err != nil {
		log.Printf("login alert: %v", err)
		return
	}
	if n > 0 {
		return
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind:     herald.KindLoginAlert,
		Severity: herald.SeverityWarning,
		Title:    title,
		Body:     body,
		Target:   herald.TargetAdmin,
		DedupKey: key,
	}); err != nil {
		log.Printf("login alert: %v", err)
	}
}

// loginLogs pages the login audit for the admin side
// (GET /admin/login-logs?page=&page_size=).
func (h *Handler) loginLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	var total int64
	if err := h.db.Model(&storage.LoginLog{}).Count(&total).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var rows []storage.LoginLog
	if err := h.db.Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "page": page, "page_size": size})
}
