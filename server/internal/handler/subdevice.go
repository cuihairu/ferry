package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// subDeviceIPWindow 是 IP 数统计的滚动窗口（P2-4）：窗口内独立 IP 计入
// ip_limit，窗口外的旧行在判定时顺带清理（小用户量懒清理足够）。
const subDeviceIPWindow = 24 * time.Hour

// subDeviceCheck 记录并校验订阅端设备/IP（P2-4）：
//   - ip 面：订阅来源 IP 总是可记（不依赖客户端），窗口内独立 IP 数超
//     ip_limit 即拒（403）；
//   - hwid 面：客户端带 X-Client-Hwid 才可记设备，设备数超 device_limit 即拒；
//     未带 hwid 的订阅请求该面不生效（客户端配合不可控，见 todo P2-4）。
// 已在册的 key（老设备/老 IP）不受收紧影响——只拦新 key，避免误伤存量。
// 超限拒访时按用户按日去重落一条 admin 告警（herald outbox）。
func (h *Handler) subDeviceCheck(c *gin.Context, u *storage.User, hwid, ip, ua string) error {
	now := time.Now()
	// IP 面（总是执行）。
	exists, err := h.subDeviceExists(u.ID, "ip", ip)
	if err != nil {
		return err
	}
	if exists {
		h.subDeviceTouch(u.ID, "ip", ip, ua, now) // 老 IP 续期（懒更新，失败不阻断）
	} else {
		if u.IPLimit > 0 {
			h.pruneSubDevices(u.ID, now)
			var cnt int64
			if err := h.db.Model(&storage.SubDevice{}).
				Where("user_id = ? AND kind = ? AND last_seen > ?", u.ID, "ip", now.Add(-subDeviceIPWindow)).
				Count(&cnt).Error; err != nil {
				return err
			}
			if cnt >= int64(u.IPLimit) {
				h.alertSubDeviceLimit(u, "ip", ip)
				return fmt.Errorf("ip limit reached (%d)", u.IPLimit)
			}
		}
		if err := h.subDeviceTouch(u.ID, "ip", ip, ua, now); err != nil {
			return err
		}
	}
	// HWID 面（客户端上报才生效）。
	if hwid != "" {
		exists, err := h.subDeviceExists(u.ID, "hwid", hwid)
		if err != nil {
			return err
		}
		if exists {
			h.subDeviceTouch(u.ID, "hwid", hwid, ua, now)
		} else {
			if u.DeviceLimit > 0 {
				var cnt int64
				if err := h.db.Model(&storage.SubDevice{}).
					Where("user_id = ? AND kind = ?", u.ID, "hwid").
					Count(&cnt).Error; err != nil {
					return err
				}
				if cnt >= int64(u.DeviceLimit) {
					h.alertSubDeviceLimit(u, "hwid", hwid)
					return fmt.Errorf("device limit reached (%d)", u.DeviceLimit)
				}
			}
			if err := h.subDeviceTouch(u.ID, "hwid", hwid, ua, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// listUserDevices 列出某用户的设备/IP 记录（GET /api/users/:id/devices）。
func (h *Handler) listUserDevices(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var rows []storage.SubDevice
	if err := h.db.Where("user_id = ?", u.ID).Order("last_seen DESC").Limit(200).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if rows == nil {
		rows = []storage.SubDevice{}
	}
	c.JSON(http.StatusOK, rows)
}

// subDeviceExists 查某 user 名下某 kind/key 是否已在册。
func (h *Handler) subDeviceExists(userID uint, kind, key string) (bool, error) {
	var cnt int64
	err := h.db.Model(&storage.SubDevice{}).
		Where("user_id = ? AND kind = ? AND key = ?", userID, kind, key).
		Count(&cnt).Error
	return cnt > 0, err
}

// subDeviceTouch 落一条设备/IP 记录（首访新建，已有则续期 last_seen）；
// 并发首访撞唯一索引按已存在处理。
func (h *Handler) subDeviceTouch(userID uint, kind, key, ua string, now time.Time) error {
	err := h.db.Create(&storage.SubDevice{
		UserID: userID, Kind: kind, Key: key, UserAgent: ua,
		FirstSeen: now, LastSeen: now,
	}).Error
	if err == nil {
		return nil
	}
	if !isDup(err) {
		return err
	}
	return h.db.Model(&storage.SubDevice{}).
		Where("user_id = ? AND kind = ? AND key = ?", userID, kind, key).
		Updates(map[string]any{"last_seen": now, "user_agent": ua}).Error
}

// pruneSubDevices 清某用户窗口外的 IP 行（hwid 行是设备集合，不随窗口清）。
func (h *Handler) pruneSubDevices(userID uint, now time.Time) {
	h.db.Where("user_id = ? AND kind = ? AND last_seen < ?", userID, "ip", now.Add(-subDeviceIPWindow)).
		Delete(&storage.SubDevice{})
}

// alertSubDeviceLimit 落一条设备/IP 超限告警（按用户按日去重，不刷屏）。
func (h *Handler) alertSubDeviceLimit(u *storage.User, kind, key string) {
	dedup := fmt.Sprintf("device_limit:%d:%s", u.ID, time.Now().Format("2006-01-02"))
	var n int64
	if err := h.db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindDeviceLimit, dedup).
		Count(&n).Error; err != nil {
		return
	}
	if n > 0 {
		return
	}
	_, _ = herald.Emit(h.db, herald.EmitInput{
		Kind:     herald.KindDeviceLimit,
		Severity: herald.SeverityWarning,
		Title:    "订阅设备/IP 超限",
		Body:     fmt.Sprintf("用户 %s（id=%d）触发 %s 上限，新来源 %s 被拒。", u.Username, u.ID, kind, key),
		Target:   herald.TargetAdmin,
		DedupKey: dedup,
		Meta:     map[string]any{"user_id": u.ID, "kind": kind, "key": key},
	})
}

// validateSubLimit 校验设备数/IP 上限取值（P2-4）：0=不限，正数上限 1000。
func validateSubLimit(deviceLimit, ipLimit *int) error {
	if deviceLimit != nil && (*deviceLimit < 0 || *deviceLimit > 1000) {
		return errors.New("device_limit must be 0-1000 (0 = unlimited)")
	}
	if ipLimit != nil && (*ipLimit < 0 || *ipLimit > 1000) {
		return errors.New("ip_limit must be 0-1000 (0 = unlimited)")
	}
	return nil
}
