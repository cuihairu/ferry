package handler

// 用户批量导入/导出（用户迁移面）：export 与 listUsers 同字段口径（含订阅
// 令牌，不含口令——口令不迁移）；import 批量建号——校验失败整批 400 零
// 写入，重名跳过并回报，成功行回订阅令牌（可选沿用旧令牌保址，缺省新生成）。

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

// maxImportRows 是单次导入行数上限（防超大载荷拖垮请求与事务）。
const maxImportRows = 500

// importRow 是导入一行：与创建用户同形（模板开关沿用，缺省不套），额外可选
// 收 sub_token——迁移保址：导出含订阅令牌，导入沿用即订阅地址不换（用户无感）；
// 不传则逐行生成新令牌。令牌全局唯一（users.uniqueIndex），重复行拒。
type importRow struct {
	model.UserInput
	SubToken string `json:"sub_token,omitempty"`
}

// importRequest 是一次导入请求。
type importRequest struct {
	Users []importRow `json:"users"`
}

// importSkip 是一行被跳过/拒绝的原因。
type importSkip struct {
	Username string `json:"username"`
	Reason   string `json:"reason"`
}

// exportUsers 导出全部用户（GET /api/users/export）：备份与跨面板迁出；
// 字段与 listUsers 完全一致（额外带 count），口令不导出（json:"-" 天然不带）。
func (h *Handler) exportUsers(c *gin.Context) {
	var users []storage.User
	if err := h.db.Order("id").Find(&users).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users, "count": len(users)})
}

// importUsers 批量导入用户（POST /api/users/import）：先全量校验（任一坏行
// 整批 400、零写入），再在事务内逐行落库（重名跳过）；返回导入成功行
// （含新生成的订阅令牌）与被跳过行及原因。子管理员受 max_users 批量约束。
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
	// 先校验后写入：坏行不落库、整批 400 并报该行原因。
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
	// 沿用令牌须全局未占用（users.sub_token uniqueIndex 兜底并发竞态）。
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

// importReject 校验期整批 400：零写入，报坏行原因（行号随 err 文案）。
func importReject(c *gin.Context, username string, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"created": 0, "skipped": 1, "skipped_rows": []importSkip{{Username: strings.TrimSpace(username), Reason: err.Error()}}})
}

// checkImportTokens 校验沿用令牌未被占用：占用时返回该行用户名与原因（调用方
// 整批 400）；db 侧错误返回空用户名（调用方按 500 处理）。
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

// validateImportRow 校验导入一行（与创建用户同口径）。
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

// checkImportQuota 子管理员批量导入配额：名下现有数 + 本批行数 ≤ max_users
// （0=不限），超出整批 403（不做部分导入，避免配额语义含糊）。
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

// txImportUsers 在事务内逐行建号：重名跳过（回报原因），订阅令牌逐行新生成；
// enabled=false 显式补写（同创建口径，防 GORM 略过零值落默认真）。
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
			token := rows[i].SubToken // 沿用旧令牌=迁移保址；缺省新生成
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

// anyWantTemplate 任一行要求套用户模板时才读一次模板设置。
func anyWantTemplate(rows []importRow) bool {
	for i := range rows {
		if rows[i].UseTemplate != nil && *rows[i].UseTemplate {
			return true
		}
	}
	return false
}
