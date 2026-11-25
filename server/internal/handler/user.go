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
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// userOnlineWindow is the G2 online-judgement window: agents default to a 30s heartbeat / 60s
// traffic report, so a 3-minute threshold tolerates one lost report without mistaking a long-dead
// connection for online.
const userOnlineWindow = 3 * time.Minute

// userView is the output view for the user list/detail: the storage row plus online state and the
// most recent node name.
type userView struct {
	storage.User
	Online   bool   `json:"online"`
	LastNode string `json:"last_node,omitempty"`
	// Split gate state (G3): Status is the display-ready primary cause
	// (active/expired/limited/disabled), while Expired/Limited keep the two
	// independent judgments so support can tell an expired user from an
	// exhausted one.
	Status  string `json:"status"`
	Expired bool   `json:"expired"`
	Limited bool   `json:"limited"`
}

// userViews converts storage rows to views in bulk: one query fills in the most recent node names.
func (h *Handler) userViews(users []storage.User) ([]userView, error) {
	out := make([]userView, 0, len(users))
	// Collect the most recent node IDs and fetch the names with a single IN query.
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
		// G3 quota leg: the judgment needs the cycle usage, but only when a
		// positive quota exists (quota==0 is unlimited and can never be
		// limited). One usage query per quota-bearing user, the same shape as
		// the distributor customer list; panel scale keeps that affordable.
		var used int64
		if u.QuotaBytes > 0 {
			var agg struct{ Rx, Tx int64 }
			if err := h.usageQuery(u.ID, u.ResetCycle, now).
				Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
				Scan(&agg).Error; err != nil {
				out = nil
				return nil, err
			}
			used = agg.Rx + agg.Tx
		}
		st := sub.UserStateOf(&u, used, now)
		v.Status, v.Expired, v.Limited = st.Status, st.Expired, st.Limited
		out = append(out, v)
	}
	return out, nil
}

func (h *Handler) listUsers(c *gin.Context) {
	out := []storage.User{}
	if err := h.db.Order("id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	views, err := h.userViews(out)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, views)
}

func (h *Handler) getUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	views, err := h.userViews([]storage.User{u})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, views[0])
}

func (h *Handler) createUser(c *gin.Context) {
	var in model.UserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// Apply the default template (P1-5): fill in absent fields first, then run the same validation
	// and persistence pipeline as before.
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
	// Sub-admin user creation quota (P2-2): an operator is bound by their own max_users and
	// counted by created_by (only their own creations count); exceeding the quota gives 403.
	// super is unlimited.
	// The api group goes through apiAuth, so the operator name comes from apiUser (= the admin
	// JWT Subject).
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
		// Enabled carries default:true, so the zero value false is skipped by GORM in favor of the DB default; write it explicitly.
		if err := h.db.Model(&storage.User{}).Where("id=?", u.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		u.Enabled = false
	}
	views, err := h.userViews([]storage.User{u})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, views[0])
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
	// renamedFrom is the pre-rename name (P2 missed-traffic fix): until the node-side config is
	// re-pushed after the rename, the old email keeps reporting, so the old name stays as an
	// alias for saveUserTraffic to keep attributing traffic.
	renamedFrom := ""
	if strings.TrimSpace(in.Username) != "" {
		// A non-empty Username counts as a rename (PATCH semantics; an empty string leaves it unchanged).
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
		// PATCH semantics: only explicitly given fields change, absent ones stay put (derefInt only reads a value, it does not zero it).
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
		// PATCH semantics as with the bandwidth limits: only explicitly given fields change.
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
			// Rename: keep the old name as an alias (P2 missed traffic). An existing alias with the
			// same name is not overwritten (when a name changes owner the original attribution is
			// kept, and the current username match takes precedence over aliases — see
			// saveUserTraffic); a concurrent unique-constraint hit counts as already present and
			// does not block the rename.
			// The alias and the rename share one transaction, so a half-finished change always rolls back.
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
	// Device/IP records are a security state (not an accounting ledger), so they are cleared with
	// the user rather than left as orphan rows.
	// Billing history (the traffic_logs accounting detail) is neither deleted nor altered (per
	// quota.go): before deleting a user, user_id is nulled first to release the attribution, so a
	// DB-level cascade (an older DB's CASCADE constraint) cannot wipe the accounting history along
	// with it — deleting a user only detaches attribution; the ledger is retained. Wrapped in a
	// transaction: detaching and deleting the row are atomic.
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

// resetSubToken resets the subscription token: the old link stops working immediately (per Security Design §4).
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
	// A reissued token voids the old link, so device/IP records are cleared too (older devices must re-register).
	if err := h.db.Where("user_id=?", u.ID).Delete(&storage.SubDevice{}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	u.SubToken = token
	views, err := h.userViews([]storage.User{u})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, views[0])
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

// errUsernameTaken indicates a duplicate username.
var errUsernameTaken = errors.New("username already exists")

// ensureUsernameFree checks that the username is not already taken by another user.
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
		return 0 // 0 = unlimited
	}
	return *p
}

// resetCycleOrDefault normalizes the reset cycle: empty/absent means none, and an invalid value is an error.
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
