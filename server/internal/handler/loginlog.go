package handler

// 管理员登录审计（安全设计 §1，P1）：每次登录尝试落 login_logs（时间/IP/
// UA/结果，对齐设计 DDL），dash 分页可查；连续失败与新网段成功登录经
// Herald login_alert 告警——未配 Herald 时事件落本地 outbox（dash 事件页
// 与站内信可见），不静默、不自建通道。

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

// 连续失败判定窗口与阈值，对齐登录限速口径（15 分钟失败 5 次锁定）。
const (
	loginBruteWindow = 15 * time.Minute
	loginBruteFailN  = 5
	loginLogUALimit  = 255
)

// writeLoginLog 落一行登录审计；失败只记日志，不阻断登录响应。
func (h *Handler) writeLoginLog(username, ip, ua string, ok bool) storage.LoginLog {
	if len(ua) > loginLogUALimit {
		ua = ua[:loginLogUALimit]
	}
	row := storage.LoginLog{Username: username, IP: ip, UA: ua, OK: ok, CreatedAt: time.Now()}
	if err := h.db.Create(&row).Error; err != nil {
		log.Printf("login log: %v", err)
	}
	return row
}

// alertLoginBrute 连续失败告警：同 IP 窗口内失败达阈值发 login_alert，
// dedup 按 IP+日（同日重复爆发不刷屏）。
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

// alertLoginNewIP 新网段成功登录告警：与该账号上次成功登录 IP 不同网段
// （v4 /24、v6 /64 粒度）时提示；首次成功登录无基线不判异地。afterID 是本次
// 成功行 ID（排除自身取上一条）。dedup 按用户+日。
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

// sameNetwork 判定两 IP 是否同网段（「异地」粒度：IPv4 /24、IPv6 /64）；
// 解析失败回退整串比对。
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

// emitLoginAlert 落 login_alert 事件（warning/admin）：本地按 dedup_key
// 自查重（同键当日至多一条——Emit 只透传不做本地去重），dedup_key 再供
// Herald 侧窗口合并；未配 Herald 时事件落本地 outbox 站内可见。
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

// loginLogs 管理端分页查询登录审计（GET /admin/login-logs?page=&page_size=）。
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
