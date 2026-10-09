package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
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
