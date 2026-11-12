package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// userOnlineWindow 是 G2 在线判定窗口：agent 默认 30s 心跳 / 60s 流量上报，
// 取 3 分钟阈值可容忍一次上报丢失，又不把「已断连许久」误判为在线。
const userOnlineWindow = 3 * time.Minute

// userView 是用户列表/详情的输出视图：在存储行上补在线态与最近接入节点名。
type userView struct {
	storage.User
	Online   bool   `json:"online"`
	LastNode string `json:"last_node,omitempty"`
}

// userViews 批量把存储行转视图：一次查询补齐最近接入节点名。
func (h *Handler) userViews(users []storage.User) []userView {
	out := make([]userView, 0, len(users))
	// 收集最近接入节点 ID，一次 IN 查询取名称。
	ids := map[uint]bool{}
	for _, u := range users {
		if u.LastNodeID != nil {
			ids[*u.LastNodeID] = true
		}
	}
	names := map[uint]string{}
	if len(ids) > 0 {
		var nodes []storage.Node
		q := make([]uint, 0, len(ids))
		for id := range ids {
			q = append(q, id)
		}
		if err := h.db.Select("id, name").Where("id IN ?", q).Find(&nodes).Error; err == nil {
			for _, n := range nodes {
				names[n.ID] = n.Name
			}
		}
	}
	now := time.Now()
	for _, u := range users {
		v := userView{User: u}
		if u.LastSeenAt != nil && now.Sub(*u.LastSeenAt) <= userOnlineWindow {
			v.Online = true
		}
		if u.LastNodeID != nil {
			v.LastNode = names[*u.LastNodeID]
		}
		out = append(out, v)
	}
	return out
}

func (h *Handler) listUsers(c *gin.Context) {
	out := []storage.User{}
	if err := h.db.Order("id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, h.userViews(out))
}

func (h *Handler) getUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	c.JSON(http.StatusOK, h.userViews([]storage.User{u})[0])
}

func (h *Handler) createUser(c *gin.Context) {
	var in model.UserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// 套用默认模板（P1-5）：先补全缺席字段，后续校验与落库走同一管线。
	if in.UseTemplate != nil && *in.UseTemplate {
		tpl, err := h.loadUserTemplate()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		applyUserTemplate(&in, tpl, time.Now())
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
		return
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		fail(c, http.StatusBadRequest, errors.New("quota_bytes must be >= 0 (0 = unlimited)"))
		return
	}
	if err := validateUserBw(in.BwUpMbps, in.BwDownMbps); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateSubLimit(in.DeviceLimit, in.IPLimit); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	cycle, err := resetCycleOrDefault(in.ResetCycle)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// 子管理员建用户配额（P2-2）：operator 受自身 max_users 约束，按
	// created_by 归账计数（只数自己建的），超配额 403；super 不受限。
	// api 组经 apiAuth，操作者名取 apiUser（=管理员 JWT Subject）。
	operator := ""
	if actor, ok := c.Get("apiUser"); ok {
		if s, isStr := actor.(string); isStr {
			operator = s
		}
	}
	if role, _ := c.Get("adminRole"); role == RoleOperator && operator != "" {
		if err := h.checkOperatorQuota(operator); err != nil {
			fail(c, http.StatusForbidden, err)
			return
		}
	}
	if err := h.ensureUsernameFree(username, 0); err != nil {
		if errors.Is(err, errUsernameTaken) {
			fail(c, http.StatusConflict, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	u := storage.User{
		Username:    username,
		SubToken:    token,
		QuotaBytes:  quotaOrDefault(in.QuotaBytes),
		ResetCycle:  cycle,
		ExpiresAt:   in.ExpiresAt,
		Enabled:     enabled,
		CreatedBy:   operator,
		BwUpMbps:    derefInt(in.BwUpMbps),
		BwDownMbps:  derefInt(in.BwDownMbps),
		DeviceLimit: derefInt(in.DeviceLimit),
		IPLimit:     derefInt(in.IPLimit),
	}
	if err := h.db.Create(&u).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("username already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !enabled {
		// Enabled 带 default:true，零值 false 会被 GORM 略过落库默认值，需显式补写。
		if err := h.db.Model(&storage.User{}).Where("id=?", u.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		u.Enabled = false
	}
	c.JSON(http.StatusCreated, h.userViews([]storage.User{u})[0])
}

func (h *Handler) updateUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in model.UserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	// renamedFrom 是改名前的旧名（P2 流量漏计修复）：改名后节点侧配置未
	// 重推期间旧 email 仍在上报，旧名留作别名供 saveUserTraffic 继续归属。
	renamedFrom := ""
	if strings.TrimSpace(in.Username) != "" {
		// Username 非空即视为改名（PATCH 语义，空串不改）。
		username := strings.TrimSpace(in.Username)
		if len(username) > 64 {
			fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
			return
		}
		if err := h.ensureUsernameFree(username, u.ID); err != nil {
			if errors.Is(err, errUsernameTaken) {
				fail(c, http.StatusConflict, err)
				return
			}
			fail(c, http.StatusInternalServerError, err)
			return
		}
		if username != u.Username {
			renamedFrom = u.Username
			updates["username"] = username
		}
	}
	if in.QuotaBytes != nil {
		if *in.QuotaBytes < 0 {
			fail(c, http.StatusBadRequest, errors.New("quota_bytes must be >= 0 (0 = unlimited)"))
			return
		}
		updates["quota_bytes"] = *in.QuotaBytes
	}
	if in.BwUpMbps != nil || in.BwDownMbps != nil {
		if err := validateUserBw(in.BwUpMbps, in.BwDownMbps); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		// PATCH 语义：只改显式给的字段，缺席不动（derefInt 仅取值不置零）。
		if in.BwUpMbps != nil {
			updates["bw_up_mbps"] = *in.BwUpMbps
		}
		if in.BwDownMbps != nil {
			updates["bw_down_mbps"] = *in.BwDownMbps
		}
	}
	if in.DeviceLimit != nil || in.IPLimit != nil {
		if err := validateSubLimit(in.DeviceLimit, in.IPLimit); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		// PATCH 语义同带宽限额：只改显式给的字段。
		if in.DeviceLimit != nil {
			updates["device_limit"] = *in.DeviceLimit
		}
		if in.IPLimit != nil {
			updates["ip_limit"] = *in.IPLimit
		}
	}
	if in.ResetCycle != nil {
		cycle, err := resetCycleOrDefault(in.ResetCycle)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updates["reset_cycle"] = cycle
	}
	if in.ExpiresAt != nil {
		updates["expires_at"] = *in.ExpiresAt
	}
	if in.ClearExpires != nil && *in.ClearExpires {
		updates["expires_at"] = nil
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if len(updates) > 0 {
		err := h.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&storage.User{}).Where("id=?", u.ID).Updates(updates).Error; err != nil {
				return err
			}
			// 改名：旧名留别名（P2 流量漏计）。已存在的同名别名不覆盖
			//（同名换主时保留原归属，当前用户名匹配优先于别名，见
			// saveUserTraffic）；并发撞唯一约束视为已留，不阻断改名。
			// 别名与改名同事务，改一半必回滚。
			if renamedFrom != "" {
				var cnt int64
				if err := tx.Model(&storage.UserAlias{}).
					Where("username = ?", renamedFrom).Count(&cnt).Error; err != nil {
					return err
				}
				if cnt == 0 {
					if err := tx.Create(&storage.UserAlias{
						UserID: u.ID, Username: renamedFrom,
					}).Error; err != nil && !isDup(err) {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			if isDup(err) {
				fail(c, http.StatusConflict, errors.New("username already exists"))
				return
			}
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	fresh, err := h.findUser(strconv.FormatUint(uint64(u.ID), 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, fresh)
}

func (h *Handler) deleteUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	// 设备/IP 记录是安全态（非账目流水），随用户一并清掉不留孤儿行。
	// 计费历史（traffic_logs 记账明细）不删不改（口径 quota.go）：删除用户前
	// 先把 user_id 置空解除归属，避免 DB 级联（旧库 CASCADE 约束）把账目历史
	// 一并删掉——删除用户只摘归属，账目留存。事务包裹：摘除与删行原子。
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", u.ID).Delete(&storage.SubDevice{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&storage.TrafficLog{}).Where("user_id = ?", u.ID).
			Updates(map[string]any{"user_id": nil}).Error; err != nil {
			return err
		}
		return tx.Delete(&u).Error
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": u.ID})
}

// resetSubToken 重置订阅令牌：旧链接立即失效（对齐安全设计 §4）。
func (h *Handler) resetSubToken(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.User{}).Where("id=?", u.ID).Update("sub_token", token).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// 令牌换发=旧链接作废，设备/IP 记录随之清空（老设备需重新在册）。
	if err := h.db.Where("user_id=?", u.ID).Delete(&storage.SubDevice{}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	u.SubToken = token
	c.JSON(http.StatusOK, h.userViews([]storage.User{u})[0])
}

func (h *Handler) findUser(id string) (storage.User, error) {
	var u storage.User
	uid, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return u, errNotFound
	}
	if err := h.db.First(&u, uid).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return u, errNotFound
		}
		return u, err
	}
	return u, nil
}

// errUsernameTaken 表示用户名重复。
var errUsernameTaken = errors.New("username already exists")

// ensureUsernameFree 检查用户名未被其他用户占用。
func (h *Handler) ensureUsernameFree(username string, selfID uint) error {
	var cnt int64
	q := h.db.Model(&storage.User{}).Where("username=?", username)
	if selfID > 0 {
		q = q.Where("id<>?", selfID)
	}
	if err := q.Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return errUsernameTaken
	}
	return nil
}

func quotaOrDefault(p *int64) int64 {
	if p == nil {
		return 0 // 0 = 不限
	}
	return *p
}

// resetCycleOrDefault 归一化重置周期：空/缺席=none，非法值报错。
func resetCycleOrDefault(p *string) (string, error) {
	if p == nil || *p == "" {
		return quota.CycleNone, nil
	}
	if !quota.ValidCycle(*p) {
		return "", errors.New("reset_cycle must be none/day/week/month")
	}
	return *p, nil
}

func isDup(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}
