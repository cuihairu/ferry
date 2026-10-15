package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// redeemCreate 造用户与卡批次，返回用户 id、批次码列表。
func redeemCreate(t *testing.T, r *gin.Engine, username string, grant map[string]any, total int) (uint, []string) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": username, "quota_bytes": 1000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	userID := uint(u["id"].(float64))

	body := map[string]any{"name": "测试批次", "total": total}
	for k, v := range grant {
		body[k] = v
	}
	rec = doJSON(t, r, "POST", "/api/card-batches", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Codes []string `json:"codes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	return userID, created.Codes
}

func TestRedeemQuota(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "alice", map[string]any{
		"grant_type": "add_quota", "grant_value": 5 << 30,
	}, 2)

	// 兑换：配额 1000 + 5GiB
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		OrderNo    string `json:"order_no"`
		GrantType  string `json:"grant_type"`
		QuotaBytes int64  `json:"quota_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.GrantType != "add_quota" || res.QuotaBytes != 1000+5*1<<30 || res.OrderNo == "" {
		t.Fatalf("result = %+v", res)
	}

	// 三账齐备
	var orders int64
	db.Model(&storage.PaymentOrder{}).Count(&orders)
	var txns int64
	db.Model(&storage.PaymentTransaction{}).Count(&txns)
	var grants int64
	db.Model(&storage.Grant{}).Count(&grants)
	if orders != 1 || txns != 1 || grants != 1 {
		t.Fatalf("ledgers: orders=%d txns=%d grants=%d", orders, txns, grants)
	}

	// 同码重放：400 统一文案，且码行 fail_count 递增
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}
	var codeRow storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&codeRow).Error; err != nil {
		t.Fatal(err)
	}
	if codeRow.FailCount != 1 || codeRow.Status != "used" {
		t.Fatalf("code after replay = %+v", codeRow)
	}
	// 三账未新增
	db.Model(&storage.PaymentOrder{}).Count(&orders)
	if orders != 1 {
		t.Fatalf("replay created order: %d", orders)
	}
}

func TestRedeemExtendDays(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "bob", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	// bob 无到期 → 自当下起算 +30 天
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/users/"+fmt.Sprint(userID), nil)
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	exp, _ := time.Parse(time.RFC3339, u["expires_at"].(string))
	if d := time.Until(exp); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("expires_at = %v (until %v)", u["expires_at"], d)
	}
}

func TestRedeemFailureModes(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "carol", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)

	// 未知码：统一文案
	rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "XXXX-XXXX-XXXX", "user_id": userID})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("unknown code: %d %s", rec.Code, rec.Body)
	}
	// 未知用户：统一文案（防枚举）
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": 99})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}
	// 未知用户的失败不误伤：码仍可用
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("code should still redeem: %d %s", rec.Code, rec.Body)
	}

	// 过期批次：建批后把有效期改到过去（模拟时间流逝），兑换应失败且回滚核销
	user2, codes2 := redeemCreate(t, r, "dave", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	past := time.Now().Add(-time.Hour)
	if err := db.Model(&storage.CardBatch{}).Where("id = 2").Update("expired_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes2[0], "user_id": user2}); rec.Code != http.StatusBadRequest {
		t.Fatalf("expired batch: %d", rec.Code)
	}
	var row storage.CardCode
	if err := db.Where("code = ?", codes2[0]).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "unused" || row.UsedBy != nil {
		t.Fatalf("expired redeem should roll back claim: %+v", row)
	}
}

// doJSONFrom 以指定来源 IP 发请求（限流按 IP 计，用不同 IP 隔离场景）。
func doJSONFrom(t *testing.T, r *gin.Engine, ip, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = ip + ":1234"
	req.Header.Set("Content-Type", "application/json")
	// /api/redeem 在管理数据面（apiAuth）：与 doJSON 同带管理员 JWT。
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRedeemDisabledByFails(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "erin", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 2)
	// 对 unused 码反复用错误用户尝试：每次核销回滚并计一次失败，5 次后禁用
	for i := 0; i < 5; i++ {
		rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[1], "user_id": 99})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	var row storage.CardCode
	if err := db.Where("code = ?", codes[1]).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "disabled" || row.FailCount < 5 {
		t.Fatalf("unused code should be disabled after 5 fails: %+v", row)
	}

	// 换 IP：正常兑换 codes[0] 后重放。已用码只累计计数不改状态（used 语义保留）。
	if rec := doJSONFrom(t, r, "203.0.113.9", "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusOK {
		t.Fatalf("fresh-ip redeem: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < 5; i++ {
		if rec := doJSONFrom(t, r, "203.0.113.9", "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusBadRequest {
			t.Fatalf("replay %d: %d", i, rec.Code)
		}
	}
	var used storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&used).Error; err != nil {
		t.Fatal(err)
	}
	if used.Status != "used" || used.FailCount < 5 {
		t.Fatalf("used code keeps status, counts fails: %+v", used)
	}
}

func TestDisableCardCode(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "grace", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 2)

	var first storage.CardCode
	if err := db.Where("code = ?", codes[0]).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	// 禁用 unused 码：成功且状态落库
	rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(first.ID)+"/disable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	var after storage.CardCode
	db.Where("code = ?", codes[0]).First(&after)
	if after.Status != "disabled" {
		t.Fatalf("status = %s", after.Status)
	}
	// 禁用后的码不可兑换，兑换走统一失败文案
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("redeem disabled: %d", rec.Code)
	}
	// 再禁一次（disabled 终态）：409
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(first.ID)+"/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-disable: %d %s", rec.Code, rec.Body)
	}
	// 已用码不可禁用：409
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[1], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	var second storage.CardCode
	db.Where("code = ?", codes[1]).First(&second)
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/"+fmt.Sprint(second.ID)+"/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("disable used: %d %s", rec.Code, rec.Body)
	}
	// 未知 id：与不可禁用同归 409（防枚举口径，见 card_test.go）
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/999/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("missing: %d", rec.Code)
	}
}

// TestRedeemEdges 覆盖兑换负例与文案边界：坏 JSON/空码/缺用户 400 统一文案、
// MB 档发放通知文案、有到期用户的时长续期从现有到期起算。
func TestRedeemEdges(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "edge-a", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)

	// 坏 JSON → 400 统一文案（不暴露解析细节）。
	if rec := doRaw(t, r, "POST", "/api/redeem", "{bad"); rec.Code != http.StatusBadRequest || rec.Body.String() != `{"error":"兑换失败"}` {
		t.Fatalf("bad json: %d %s", rec.Code, rec.Body)
	}
	// 空码 / 缺用户 → 400。
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "", "user_id": userID}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "AAAA-BBBB-CCCC"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no user: %d", rec.Code)
	}

	// MB 档文案：300MB 配额发放 → 站内信标题按 MB 计。
	r2, db2 := newTestRouterWithDB(t)
	user2, codes2 := redeemCreate(t, r2, "edge-b", map[string]any{
		"grant_type": "add_quota", "grant_value": 300 << 20,
	}, 1)
	if rec := doJSON(t, r2, "POST", "/api/redeem", map[string]any{"code": codes2[0], "user_id": user2}); rec.Code != http.StatusOK {
		t.Fatalf("mb redeem: %d %s", rec.Code, rec.Body)
	}
	var note storage.Notification
	if err := db2.Where("user_id = ?", user2).First(&note).Error; err != nil {
		t.Fatal(err)
	}
	if note.Title != "流量已到账：+300 MB" {
		t.Fatalf("notification title = %q", note.Title)
	}

	// 有到期用户：时长续期从现有到期起算（+10 天再 +30 → 约 +40 天）。
	r3, db3 := newTestRouterWithDB(t)
	user3, codes3 := redeemCreate(t, r3, "edge-c", map[string]any{
		"grant_type": "extend_days", "grant_value": 30,
	}, 1)
	if err := db3.Model(&storage.User{}).Where("id = ?", user3).
		Update("expires_at", time.Now().Add(10*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r3, "POST", "/api/redeem", map[string]any{"code": codes3[0], "user_id": user3}); rec.Code != http.StatusOK {
		t.Fatalf("extend redeem: %d %s", rec.Code, rec.Body)
	}
	var eu storage.User
	if err := db3.First(&eu, user3).Error; err != nil {
		t.Fatal(err)
	}
	if d := time.Until(*eu.ExpiresAt); d < 39*24*time.Hour || d > 41*24*time.Hour {
		t.Fatalf("expires_at until = %v, want ~40d", d)
	}
}

// TestRedeemDBErrors 覆盖兑换事务各步取数/落账失败 → 500 且整体回滚零写入
// （gorm 回调注错，对齐既有口径）；另含删批次与坏代理引用两种 500。
func TestRedeemDBErrors(t *testing.T) {
	scenarios := []struct {
		name string
		reg  func(db *gorm.DB)
	}{
		{"order create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:order", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.PaymentOrder); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"ledger create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:ledger", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*[]storage.DistributorLedger); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"txn create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:txn", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.PaymentTransaction); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"grant create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:grant", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.Grant); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"notification create", func(db *gorm.DB) {
			db.Callback().Create().Before("gorm:create").Register("t:note", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.Notification); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"user update", func(db *gorm.DB) {
			db.Callback().Update().Before("gorm:update").Register("t:userupd", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Model.(*storage.User); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"code read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:codeload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.CardCode); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"user read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:userload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.User); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"batch read", func(db *gorm.DB) {
			db.Callback().Query().Before("gorm:query").Register("t:batchload", func(tx *gorm.DB) {
				if _, ok := tx.Statement.Dest.(*storage.CardBatch); ok {
					tx.AddError(errors.New("db down"))
				}
			})
		}},
		{"distributor missing", nil},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			userID, codes := redeemCreate(t, r, "dbx", map[string]any{
				"grant_type": "add_quota", "grant_value": 1 << 30,
			}, 1)
			var codeRow storage.CardCode
			if err := db.Where("code = ?", codes[0]).First(&codeRow).Error; err != nil {
				t.Fatal(err)
			}
			switch sc.name {
			case "ledger create":
				// 真实代理：批次挂代理带面价，佣金行照常记账。
				rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{
					"username": "dbx-dist", "password": "secret123", "discount_percent": 30,
				})
				if rec.Code != http.StatusCreated {
					t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
				}
				var created struct {
					Distributor map[string]any `json:"distributor"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &created)
				if err := db.Model(&storage.CardBatch{}).Where("id = ?", codeRow.BatchID).
					Updates(map[string]any{
						"distributor_id": uint(created.Distributor["id"].(float64)),
						"price_cents":    100,
					}).Error; err != nil {
					t.Fatal(err)
				}
			case "distributor missing":
				if err := db.Model(&storage.CardBatch{}).Where("id = ?", codeRow.BatchID).
					Updates(map[string]any{"distributor_id": 999, "price_cents": 100}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if sc.reg != nil {
				sc.reg(db)
			}
			rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": codes[0], "user_id": userID})
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
			}
			// 事务回滚零写入：无订单无发放。
			var orders, grants int64
			db.Model(&storage.PaymentOrder{}).Count(&orders)
			db.Model(&storage.Grant{}).Count(&grants)
			if orders != 0 || grants != 0 {
				t.Fatalf("leaked rows: orders=%d grants=%d", orders, grants)
			}
		})
	}
}

func TestRedeemRateLimited(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "frank", map[string]any{
		"grant_type": "add_quota", "grant_value": 1 << 30,
	}, 1)
	// 连续 10 次尝试占满窗口，第 11 次 429
	sawRateLimit := false
	for i := 0; i < 11; i++ {
		rec := doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": "AAAA-BBBB-CCCC", "user_id": userID})
		if rec.Code == http.StatusTooManyRequests {
			sawRateLimit = true
			break
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if !sawRateLimit {
		t.Fatal("expected 429 within 11 attempts")
	}
}
