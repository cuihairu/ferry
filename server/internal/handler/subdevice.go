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

// subDeviceIPWindow is the rolling window for IP counting (P2-4): distinct IPs inside
// the window count against ip_limit, and stale rows outside it are cleaned up as a
// side effect of each verdict (lazy pruning is enough at this user scale).
const subDeviceIPWindow = 24 * time.Hour

// subDeviceCheck records and checks subscription devices/IPs (P2-4):
//   - ip facet: the subscription source IP can always be recorded (client-independent);
//     distinct IPs inside the window exceeding ip_limit are rejected (403);
//   - hwid facet: a device can only be recorded when the client sends X-Client-Hwid,
//     and device counts over device_limit are rejected; subscription requests without a
//     hwid are not subject to this facet (client cooperation is out of our control, see
//     todo P2-4).
//
// Already-registered keys (existing devices/IPs) are unaffected by tightening -- only new
// keys are blocked, so the existing base is not caught in the crossfire.
// Over-limit rejections land one admin alert per user per day (herald outbox, deduped).
func (h *Handler) subDeviceCheck(c *gin.Context, u *storage.User, hwid, ip, ua string) error {
	now := time.Now()
	// IP facet (always runs).
	exists, err := h.subDeviceExists(u.ID, "ip", ip)
	if err != nil {
		return err
	}
	if exists {
		h.subDeviceTouch(u.ID, "ip", ip, ua, now) // renew an existing IP (lazy update, failures do not block)
	} else {
		// Rows outside the window are always pruned here (P1-7): unlimited users
		// (IPLimit=0) never had their window cleared before, so sub_devices grew
		// without bound; pruning once per new IP visit keeps the table bounded.
		h.pruneSubDevices(u.ID, now)
		if u.IPLimit > 0 {
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
	// HWID facet (only effective when the client reports one).
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

// listUserDevices lists a user's device/IP records (GET /api/users/:id/devices).
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

// subDeviceExists reports whether a kind/key is already registered under a user.
func (h *Handler) subDeviceExists(userID uint, kind, key string) (bool, error) {
	var cnt int64
	err := h.db.Model(&storage.SubDevice{}).
		Where("user_id = ? AND kind = ? AND key = ?", userID, kind, key).
		Count(&cnt).Error
	return cnt > 0, err
}

// subDeviceTouch lands a device/IP record (created on first visit, last_seen renewed
// when it already exists); a concurrent first visit hitting the unique index is treated
// as already present.
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

// pruneSubDevices deletes a user's IP rows outside the window (hwid rows are a device set and are not cleared with the window).
func (h *Handler) pruneSubDevices(userID uint, now time.Time) {
	h.db.Where("user_id = ? AND kind = ? AND last_seen < ?", userID, "ip", now.Add(-subDeviceIPWindow)).
		Delete(&storage.SubDevice{})
}

// alertSubDeviceLimit lands a device/IP over-limit alert (deduped per user per day, so the inbox is not flooded).
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

// validateSubLimit checks device/IP limit values (P2-4): 0 means unlimited, positive values are capped at 1000.
func validateSubLimit(deviceLimit, ipLimit *int) error {
	if deviceLimit != nil && (*deviceLimit < 0 || *deviceLimit > 1000) {
		return errors.New("device_limit must be 0-1000 (0 = unlimited)")
	}
	if ipLimit != nil && (*ipLimit < 0 || *ipLimit > 1000) {
		return errors.New("ip_limit must be 0-1000 (0 = unlimited)")
	}
	return nil
}
