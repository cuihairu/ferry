package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// utoa 是测试内 id 转字符串的简写。
func utoa(id uint) string { return strconv.FormatUint(uint64(id), 10) }

// mustJSON 解码响应为 map，失败即 Fatal。
func mustJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return m
}

func TestDistributorCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 校验面：缺用户名/缺密码/佣金越界。
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"password": "x12345"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing username: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing password: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "x12345", "discount_percent": 101})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("discount>100: %d %s", rec.Code, rec.Body)
	}

	// 建号成功：默认佣金 0、启用，哈希不外泄。
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Distributor map[string]any `json:"distributor"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Distributor["password_hash"] != nil {
		t.Fatalf("password_hash leaked: %s", rec.Body)
	}
	if created.Distributor["discount_percent"].(float64) != 0 || created.Distributor["enabled"] != true {
		t.Fatalf("defaults wrong: %s", rec.Body)
	}
	id := uint(created.Distributor["id"].(float64))

	// 重复用户名 409。
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "x12345"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup username: %d %s", rec.Code, rec.Body)
	}

	// 部分更新：佣金 30 + 备注。
	rec = doJSON(t, r, "PUT", "/api/distributors/"+utoa(id), map[string]any{"discount_percent": 30, "note": "华东渠道"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.First(&d, id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.DiscountPercent != 30 || d.Note != "华东渠道" {
		t.Fatalf("update lost: %+v", d)
	}

	// 列表附余额（初始 0）。
	rec = doJSON(t, r, "GET", "/api/distributors", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Distributors []struct {
			Username     string `json:"username"`
			BalanceCents int64  `json:"balance_cents"`
		} `json:"distributors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Distributors) != 1 || list.Distributors[0].BalanceCents != 0 {
		t.Fatalf("list: %s", rec.Body)
	}

	// 流水空态：余额 0、无行。
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(id)+"/ledger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ledger: %d %s", rec.Code, rec.Body)
	}

	// 不存在的代理：更新与流水 404。
	rec = doJSON(t, r, "PUT", "/api/distributors/999", map[string]any{"enabled": false})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update 404: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/distributors/999/ledger", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ledger 404: %d %s", rec.Code, rec.Body)
	}
}

func TestDistributorLogin(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// 成功：token 非空、佣金回显。
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var login struct {
		Token           string `json:"token"`
		DiscountPercent int    `json:"discount_percent"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &login)
	if login.Token == "" {
		t.Fatalf("no token: %s", rec.Body)
	}

	// 错密/未知用户 401。
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "wrong"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "ghost", "password": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: %d %s", rec.Code, rec.Body)
	}

	// 停用即禁登录。
	rec = doJSON(t, r, "PUT", "/api/distributors/"+utoa(id), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login: %d %s", rec.Code, rec.Body)
	}
}

func TestRedeemDistributorLedger(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, _ := redeemCreate(t, r, "alice", map[string]any{"grant_type": "add_quota", "grant_value": 1000}, 1)

	// 代理号：佣金 30%。
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123", "discount_percent": 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dist: %d %s", rec.Code, rec.Body)
	}
	distID := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// 代理批次：面价 1000 分。
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "代理批次", "grant_type": "add_quota", "grant_value": 5000,
		"total": 3, "price_cents": 1000, "distributor_id": distID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("dist batch: %d %s", rec.Code, rec.Body)
	}
	distCodes := mustJSON(t, rec)["codes"].([]any)

	// 兑换代理卡：订单带归属，sale(+1000) + commission(+300) 同 order_no。
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": distCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	orderNo := mustJSON(t, rec)["order_no"].(string)
	var order storage.PaymentOrder
	if err := db.Where("order_no = ?", orderNo).First(&order).Error; err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.DistributorID != distID {
		t.Fatalf("order distributor: %d", order.DistributorID)
	}
	var ledger []storage.DistributorLedger
	if err := db.Where("distributor_id = ?", distID).Order("kind").Find(&ledger).Error; err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(ledger) != 2 {
		t.Fatalf("want 2 ledger rows, got %d: %+v", len(ledger), ledger)
	}
	sale, commission := ledger[0], ledger[1]
	if sale.Kind == "commission" {
		sale, commission = commission, sale
	}
	if sale.Kind != "sale" || sale.AmountCents != 1000 || sale.OrderNo != orderNo {
		t.Fatalf("sale row wrong: %+v", sale)
	}
	if commission.Kind != "commission" || commission.AmountCents != 300 || commission.OrderNo != orderNo {
		t.Fatalf("commission row wrong: %+v", commission)
	}
	// 余额=佣金 300（sale 留痕不进余额）。
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(distID)+"/ledger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ledger api: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 300 {
		t.Fatalf("balance: %v %s", got, rec.Body)
	}

	// 自营批次不落账。
	_, selfCodes := redeemCreate(t, r, "bob", map[string]any{"grant_type": "add_quota", "grant_value": 1000, "price_cents": 800}, 1)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": selfCodes[0], "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("self redeem: %d %s", rec.Code, rec.Body)
	}
	var cnt int64
	db.Model(&storage.DistributorLedger{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("self-owned batch wrote ledger: %d", cnt)
	}

	// 佣金 0：只落 sale 一行。
	rec = doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d2", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create d2: %d %s", rec.Code, rec.Body)
	}
	d2 := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "零佣金批次", "grant_type": "add_quota", "grant_value": 1000,
		"total": 1, "price_cents": 500, "distributor_id": d2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("d2 batch: %d %s", rec.Code, rec.Body)
	}
	d2codes := mustJSON(t, rec)["codes"].([]any)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": d2codes[0].(string), "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("d2 redeem: %d %s", rec.Code, rec.Body)
	}
	var d2rows []storage.DistributorLedger
	db.Where("distributor_id = ?", d2).Find(&d2rows)
	if len(d2rows) != 1 || d2rows[0].Kind != "sale" || d2rows[0].AmountCents != 500 {
		t.Fatalf("d2 ledger: %+v", d2rows)
	}

	// 停用代理：卡照常兑换、分润照记（权益必兑现，账目保留）。
	if err := db.Model(&storage.Distributor{}).Where("id = ?", distID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{"code": distCodes[1].(string), "user_id": userID})
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled-dist redeem: %d %s", rec.Code, rec.Body)
	}
	var rows2 int64
	db.Model(&storage.DistributorLedger{}).Where("distributor_id = ?", distID).Count(&rows2)
	if rows2 != 4 {
		t.Fatalf("disabled dist ledger rows: %d", rows2)
	}

	// 归属指向不存在的代理：建批次 400。
	rec = doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "幽灵批次", "grant_type": "add_quota", "grant_value": 1000,
		"total": 1, "distributor_id": 999,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ghost distributor batch: %d %s", rec.Code, rec.Body)
	}

	// 对账四元组（DS-2 代理维度）：d1 两笔代理卡 → sale=2000、commission=600。
	rec = doJSON(t, r, "GET", "/api/distributors", nil)
	var list struct {
		Distributors []struct {
			Username        string `json:"username"`
			SaleCents       int64  `json:"sale_cents"`
			CommissionCents int64  `json:"commission_cents"`
			PayoutCents     int64  `json:"payout_cents"`
			BalanceCents    int64  `json:"balance_cents"`
		} `json:"distributors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	for _, d := range list.Distributors {
		if d.Username == "d1" {
			if d.SaleCents != 2000 || d.CommissionCents != 600 || d.PayoutCents != 0 || d.BalanceCents != 600 {
				t.Fatalf("d1 totals: %+v", d)
			}
		}
	}
}

func TestDistributorPayoutAdjust(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d1", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	// 空余额打款超余额拒、adjust 0 拒。
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 100})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("payout over balance: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/adjust", map[string]any{"amount_cents": 0, "note": "无效"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("adjust zero: %d %s", rec.Code, rec.Body)
	}

	// adjust +500 入账 → balance 500。
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/adjust", map[string]any{"amount_cents": 500, "note": "补偿"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("adjust: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 500 {
		t.Fatalf("balance after adjust: %v", got)
	}

	// 打款 800 超余额拒、500 恰好 → balance 0。
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 800})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("payout exceed: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/"+utoa(id)+"/payout", map[string]any{"amount_cents": 500, "note": "10 月结算"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payout: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["balance_cents"].(float64); got != 0 {
		t.Fatalf("balance after payout: %v", got)
	}

	// 流水两行、404 面。
	rec = doJSON(t, r, "GET", "/api/distributors/"+utoa(id)+"/ledger", nil)
	var led struct {
		Ledger       []map[string]any `json:"ledger"`
		BalanceCents float64          `json:"balance_cents"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &led)
	if len(led.Ledger) != 2 || led.BalanceCents != 0 {
		t.Fatalf("ledger rows: %s", rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/distributors/999/payout", map[string]any{"amount_cents": 100})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("payout 404: %d %s", rec.Code, rec.Body)
	}
}
