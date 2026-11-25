package handler

// Bulk user import/export (user migration surface): export follows the same
// field terms as listUsers (includes subscription tokens, never passwords --
// passwords do not migrate); import creates accounts in bulk -- validation
// failure means 400 for the whole batch with zero writes, duplicate names are
// skipped and reported, and successful rows get subscription tokens back
// (optionally reusing the old token to keep the address, else freshly minted).

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxImportRows caps the row count per import call (guards against oversized
// payloads dragging down the request and the transaction).
const maxImportRows = 500

// importRow is one imported row: same shape as user creation (template switch
// carried over, not applied by default), plus an optional sub_token -- address
// preservation on migration: export carries the subscription token, and
// importing it keeps the subscription address unchanged (invisible to the user);
// when absent, a fresh token is minted per row. Tokens are globally unique
// (users.uniqueIndex); duplicate rows are rejected.
type importRow struct {
	model.UserInput
	SubToken string `json:"sub_token,omitempty"`
}

// importRequest is one import call.
type importRequest struct {
	Users []importRow `json:"users"`
}

// importSkip is the reason a row was skipped/rejected.
type importSkip struct {
	Username string `json:"username"`
	Reason   string `json:"reason"`
}

// exportUsers exports all users (GET /api/users/export): backup and cross-panel
// migration out; fields match listUsers exactly (plus a count), and passwords are
// never exported (json:"-" omits them naturally).
func (h *Handler) exportUsers(c *gin.Context) {
	var users []storage.User
	if err := h.db.Order("id").Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	views := h.userViews(users)
	c.JSON(http.StatusOK, gin.H{"users": views, "count": len(views)})
}

// importUsers bulk-imports users (POST /api/users/import): validate everything
// first (any bad row means 400 for the whole batch, zero writes), then persist
// row by row inside a transaction (duplicates skipped); it returns the
// successfully imported rows (with freshly minted subscription tokens) and the
// skipped rows with their reasons. Sub-admins are bound by max_users per batch.
func (h *Handler) importUsers(c *gin.Context) {
	var req importRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(req.Users) == 0 {
		fail(c, http.StatusBadRequest, errors.New("users is required (1-500 rows)"))
		return
	}
	if len(req.Users) > maxImportRows {
		fail(c, http.StatusBadRequest, fmt.Errorf("users must be <= %d rows per call", maxImportRows))
		return
	}
	operator := ""
	if actor, ok := c.Get("apiUser"); ok {
		if s, isStr := actor.(string); isStr {
			operator = s
		}
	}
	var tpl userTemplate
	if anyWantTemplate(req.Users) {
		t, err := h.loadUserTemplate()
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		tpl = t
	}
	// Validate before writing: bad rows never land, whole batch 400s with that
	// row's reason.
	seen := make(map[string]int)
	for i := range req.Users {
		in := &req.Users[i]
		in.SubToken = strings.TrimSpace(in.SubToken)
		if in.UseTemplate != nil && *in.UseTemplate {
			applyUserTemplate(&in.UserInput, tpl, time.Now())
		}
		if err := validateImportRow(in); err != nil {
			importReject(c, in.Username, err)
			return
		}
		if in.SubToken != "" {
			if j, dup := seen[in.SubToken]; dup {
				importReject(c, in.Username, fmt.Errorf("sub_token duplicated by row #%d", j+1))
				return
			}
			seen[in.SubToken] = i
		}
	}
	// Reused tokens must be globally unoccupied (users.sub_token uniqueIndex
	// backstops concurrent races).
	if name, err := checkImportTokens(h.db, req.Users); err != nil {
		if name == "" {
			fail(c, http.StatusInternalServerError, err)
		} else {
			importReject(c, name, err)
		}
		return
	}
	if role, _ := c.Get("adminRole"); role == RoleOperator && operator != "" {
		if err := checkImportQuota(h.db, operator, len(req.Users)); err != nil {
			fail(c, http.StatusForbidden, err)
			return
		}
	}
	created, skips, err := h.txImportUsers(req.Users, operator)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"created":      len(created),
		"skipped":      len(skips),
		"users":        created,
		"skipped_rows": skips,
	})
}

// importReject is the validation-stage whole-batch 400: zero writes, reporting
// the bad row's reason (the row number travels inside the err message).
func importReject(c *gin.Context, username string, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"created": 0, "skipped": 1, "skipped_rows": []importSkip{{Username: strings.TrimSpace(username), Reason: err.Error()}}})
}

// checkImportTokens validates that reused tokens are unoccupied: on occupation it
// returns that row's username and reason (the caller 400s the whole batch); on a
// db-side error it returns an empty username (the caller treats it as 500).
func checkImportTokens(db *gorm.DB, rows []importRow) (string, error) {
	var wanted []string
	for i := range rows {
		if rows[i].SubToken != "" {
			wanted = append(wanted, rows[i].SubToken)
		}
	}
	if len(wanted) == 0 {
		return "", nil
	}
	var taken []string
	if err := db.Model(&storage.User{}).Where("sub_token IN ?", wanted).Pluck("sub_token", &taken).Error; err != nil {
		return "", err
	}
	for _, tk := range taken {
		for i := range rows {
			if rows[i].SubToken == tk {
				return rows[i].Username, fmt.Errorf("row #%d: sub_token %q already in use", i+1, tk)
			}
		}
	}
	return "", nil
}

// validateImportRow validates one imported row (same terms as user creation).
func validateImportRow(in *importRow) error {
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		return errors.New("username is required (1-64 chars)")
	}
	if len(in.SubToken) > 64 {
		return errors.New("sub_token must be <= 64 chars")
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		return errors.New("quota_bytes must be >= 0 (0 = unlimited)")
	}
	if err := validateUserBw(in.BwUpMbps, in.BwDownMbps); err != nil {
		return err
	}
	if err := validateSubLimit(in.DeviceLimit, in.IPLimit); err != nil {
		return err
	}
	_, err := resetCycleOrDefault(in.ResetCycle)
	return err
}

// checkImportQuota checks the sub-admin bulk import quota: existing count under
// the operator + this batch's rows <= max_users (0=unlimited); exceeding it
// 403s the whole batch (no partial import, which would muddy quota semantics).
func checkImportQuota(db *gorm.DB, operator string, n int) error {
	var op storage.User
	if err := db.Where("username = ? AND is_admin = ?", operator, true).First(&op).Error; err != nil {
		return errors.New("operator account not found")
	}
	if op.MaxUsers <= 0 {
		return nil
	}
	var used int64
	if err := db.Model(&storage.User{}).Where("created_by = ?", operator).Count(&used).Error; err != nil {
		return err
	}
	if used+int64(n) > int64(op.MaxUsers) {
		return fmt.Errorf("operator user quota exceeded (%d/%d)", used+int64(n), op.MaxUsers)
	}
	return nil
}

// txImportUsers creates accounts row by row inside a transaction: duplicates
// are skipped (reason reported), subscription tokens are freshly minted per row;
// enabled=false is written explicitly (same terms as creation, so GORM does not
// skip the zero value and land the default true).
func (h *Handler) txImportUsers(rows []importRow, operator string) ([]storage.User, []importSkip, error) {
	var created []storage.User
	var skips []importSkip
	err := h.db.Transaction(func(tx *gorm.DB) error {
		for i := range rows {
			username := strings.TrimSpace(rows[i].Username)
			var cnt int64
			if err := tx.Model(&storage.User{}).Where("username=?", username).Count(&cnt).Error; err != nil {
				return err
			}
			if cnt > 0 {
				skips = append(skips, importSkip{Username: username, Reason: "username already exists"})
				continue
			}
			token := rows[i].SubToken // reusing the old token = address-preserving migration; empty means freshly minted
			if token == "" {
				t, err := RandomToken()
				if err != nil {
					return err
				}
				token = t
			}
			cycle, err := resetCycleOrDefault(rows[i].ResetCycle)
			if err != nil {
				return err
			}
			enabled := true
			if rows[i].Enabled != nil {
				enabled = *rows[i].Enabled
			}
			u := storage.User{
				Username:    username,
				SubToken:    token,
				QuotaBytes:  quotaOrDefault(rows[i].QuotaBytes),
				ResetCycle:  cycle,
				ExpiresAt:   rows[i].ExpiresAt,
				Enabled:     enabled,
				CreatedBy:   operator,
				BwUpMbps:    derefInt(rows[i].BwUpMbps),
				BwDownMbps:  derefInt(rows[i].BwDownMbps),
				DeviceLimit: derefInt(rows[i].DeviceLimit),
				IPLimit:     derefInt(rows[i].IPLimit),
			}
			if err := tx.Create(&u).Error; err != nil {
				return err
			}
			if !enabled {
				if err := tx.Model(&storage.User{}).Where("id=?", u.ID).Update("enabled", false).Error; err != nil {
					return err
				}
			}
			u.Enabled = enabled
			created = append(created, u)
		}
		return nil
	})
	return created, skips, err
}

// anyWantTemplate only reads the template settings once when some row asks for
// the user template.
func anyWantTemplate(rows []importRow) bool {
	for i := range rows {
		if rows[i].UseTemplate != nil && *rows[i].UseTemplate {
			return true
		}
	}
	return false
}
