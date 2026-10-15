package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/payment"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 三十批补口：用户门户（PAY-7）各 400/404/500 口径——令牌解析库故障 500、
// 用量/设备/IP 三段计数各自失败、兑换坏绑定/空码/限速锁定/核销库故障、订单
// limit 校验与列表两段查询失败、商品列表两条查询失败（含聚合 Scan 走
// gorm:row 链）、下单身份缺失/绑定缺字段/IP 限速/定位批次库故障/余量计数
// 失败/活动查询失败/优惠码核销失败/订单落库失败、订单详情定位库故障与发放
// 查询失败、节省报表库故障。
// 留白：358 的 parseCampaignRules 事务内二次解析分支——bestCampaign 已先
// 解析同份规则并跳过失败活动，进事务的规则必然解析成功，防御性代码不可达。

// isPayOrderModel 判断语句是否落在订单表上。
func isPayOrderModel(v any) bool {
	switch v.(type) {
	case *storage.PaymentOrder, []storage.PaymentOrder, *[]storage.PaymentOrder:
		return true
	}
	return false
}

// isGrantModel 判断语句是否落在发放记录表上。
func isGrantModel(v any) bool {
	switch v.(type) {
	case *storage.Grant, []storage.Grant, *[]storage.Grant:
		return true
	}
	return false
}

// isCardBatchModel 判断语句是否落在卡密批次表上。
func isCardBatchModel(v any) bool {
	switch v.(type) {
	case *storage.CardBatch, []storage.CardBatch, *[]storage.CardBatch:
		return true
	}
	return false
}

// isCardCodeModel 判断语句是否落在卡密码表上。
func isCardCodeModel(v any) bool {
	switch v.(type) {
	case *storage.CardCode, []storage.CardCode, *[]storage.CardCode:
		return true
	}
	return false
}

// isCampaignModel 判断语句是否落在活动表上。
func isCampaignModel(v any) bool {
	switch v.(type) {
	case *storage.Campaign, []storage.Campaign, *[]storage.Campaign:
		return true
	}
	return false
}

// failQueryOn 让命中匹配的查询语句失败（Model 与 Dest 双查，覆盖批量 Find
// 只带 Dest 的形态）。
func failQueryOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failRowOn 让命中匹配的 Scan 语句失败（聚合 Scan 走 gorm:row 链）。
func failRowOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Row().Before("gorm:row").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failUpdateOn 让命中匹配的更新语句失败（码核销/优惠码核销定向）。
func failUpdateOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failCreateOn 让命中匹配的创建语句失败（订单落库定向）。
func failCreateOn(t *testing.T, db *gorm.DB, name string, match func(any) bool) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedPanelUser 直落一行可访问门户的用户，返回其 ID。
func seedPanelUser(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.User{Username: name, SubToken: name + "-sub", QuotaBytes: 1 << 30, Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed panel user %s: %v", name, err)
	}
	return row.ID
}

// seedSellableBatch 直落一行可售批次（带价有量），返回批次 ID。
func seedSellableBatch(t *testing.T, db *gorm.DB, name string, price int64) uint {
	t.Helper()
	batch := storage.CardBatch{Name: name, GrantType: "add_quota", GrantValue: 1 << 30,
		PriceCents: price, Total: 10, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("seed batch %s: %v", name, err)
	}
	code := storage.CardCode{BatchID: batch.ID, Code: name + "-CODE", Status: "unused"}
	if err := db.Create(&code).Error; err != nil {
		t.Fatalf("seed code %s: %v", name, err)
	}
	return batch.ID
}

// TestPanelUserErrors 令牌解析的库故障：非未找到类错误冒 500（缺失/未匹配/
// 停用统一 404 已在既有用例覆盖）。
func TestPanelUserErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "pu")
	closeSQL(t, db)
	if rec := doPanel(t, r, "pu-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelMeErrors 用量/设备/IP 三段计数各自失败冒 500。
func TestPanelMeErrors(t *testing.T) {
	// 用量 Scan 失败（拆流量表）。
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-usage")
	if err := db.Migrator().DropTable(&storage.TrafficLog{}); err != nil {
		t.Fatalf("drop traffic: %v", err)
	}
	if rec := doPanel(t, r, "me-usage-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}

	// 设备数失败（拆设备表）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-dev")
	if err := db.Migrator().DropTable(&storage.SubDevice{}); err != nil {
		t.Fatalf("drop subdevice: %v", err)
	}
	if rec := doPanel(t, r, "me-dev-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("devices: %d %s", rec.Code, rec.Body)
	}

	// IP 数失败：设备段计数（第 1 次）放行，IP 段计数（第 2 次）注错。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "me-ip")
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:subdevice_at", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.SubDevice); ok {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if rec := doPanel(t, r, "me-ip-sub", http.MethodGet, "/api/panel/me", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("ips: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelRedeemErrors 兑换：坏绑定、空码、五连败锁定与核销库故障。
// redeemTx 的原子核销 UPDATE 先行（查无码也执行，行数 0 才是兑换失败），
// 注错 UPDATE 即得非兑换失败的 500。
func TestPanelRedeemErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "redeemer")
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "   "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d %s", rec.Code, rec.Body)
	}

	// 连续失败五次即锁定：第六次直接 429（与 dash 侧兑换同口径）。
	for i := 0; i < 5; i++ {
		if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
			map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusBadRequest {
			t.Fatalf("fail %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := doPanel(t, r, "redeemer-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked: %d %s", rec.Code, rec.Body)
	}

	// 核销 UPDATE 失败冒 500（非统一兑换失败文案）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "redeemer2")
	failUpdateOn(t, db, "test:cardcode_update", func(v any) bool {
		_, ok := v.(*storage.CardCode)
		return ok
	})
	if rec := doPanel(t, r, "redeemer2-sub", http.MethodPost, "/api/panel/redeem",
		map[string]any{"code": "NO-SUCH-CODE"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("tx: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelOrdersErrors 订单列表：limit 校验、订单取数失败与发放查询失败。
func TestPanelOrdersErrors(t *testing.T) {
	for _, limit := range []string{"abc", "0", "500"} {
		r, db := newTestRouterWithDB(t)
		seedPanelUser(t, db, "ord-lim-"+limit)
		if rec := doPanel(t, r, "ord-lim-"+limit+"-sub", http.MethodGet, "/api/panel/orders?limit="+limit, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s: %d %s", limit, rec.Code, rec.Body)
		}
	}

	// 合法 limit 走夹取赋值分支，返回 200 空列表。
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "ord-lim-ok")
	if rec := doPanel(t, r, "ord-lim-ok-sub", http.MethodGet, "/api/panel/orders?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}

	// 订单取数失败。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "ord-q")
	failQueryOn(t, db, "test:payorder_q", isPayOrderModel)
	if rec := doPanel(t, r, "ord-q-sub", http.MethodGet, "/api/panel/orders", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("orders: %d %s", rec.Code, rec.Body)
	}

	// 发放查询失败（列表有单，循环内发放段故障）。
	r, db = newTestRouterWithDB(t)
	uid := seedPanelUser(t, db, "ord-g")
	if err := db.Create(&storage.PaymentOrder{OrderNo: "ORD-1", UserID: uid, Provider: "card",
		Product: "p", AmountCents: 100, Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	failQueryOn(t, db, "test:grant_q", isGrantModel)
	if rec := doPanel(t, r, "ord-g-sub", http.MethodGet, "/api/panel/orders", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("grants: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelProductsErrors 商品列表：身份缺失 404、批次取数失败与余量统计
// 失败（聚合 Scan 走 gorm:row 链）。
func TestPanelProductsErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	// 批次取数失败。
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "prod-b")
	failQueryOn(t, db, "test:cardbatch_q", isCardBatchModel)
	if rec := doPanel(t, r, "prod-b-sub", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batches: %d %s", rec.Code, rec.Body)
	}

	// 余量统计失败（Model 在卡密码表上）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "prod-r")
	failRowOn(t, db, "test:cardcode_row", isCardCodeModel)
	if rec := doPanel(t, r, "prod-r-sub", http.MethodGet, "/api/panel/products", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("remaining: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelCreateOrderErrors 下单：身份缺失、绑定缺字段、IP 限速、定位批
// 次库故障、余量计数失败、活动查询失败、优惠码核销失败与订单落库失败。
// 网关在批次定位之前取用，先注册假网关放行到库故障段。
func TestPanelCreateOrderErrors(t *testing.T) {
	// 网关在批次定位之前取用，先注册假网关放行到库故障段。
	payment.Register(stubGateway{})
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": 1, "provider": "epusdt"}); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "", http.MethodPost, "/api/panel/orders", "not json"); rec.Code != http.StatusNotFound {
		t.Fatalf("bind no token: %d %s", rec.Code, rec.Body)
	}

	// 绑定坏 JSON 与缺字段（带令牌；缺 batch_id 走同一 400 文案）。
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-bind")
	if rec := doPanel(t, r, "co-bind-sub", http.MethodPost, "/api/panel/orders", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doPanel(t, r, "co-bind-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"provider": "epusdt"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing batch: %d %s", rec.Code, rec.Body)
	}

	// IP 限速：10 次/分钟只 Allow，第 11 次起 429（未到网关也会先计数）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-lim")
	var rec *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		rec = doPanel(t, r, "co-lim-sub", http.MethodPost, "/api/panel/orders",
			map[string]any{"batch_id": 1, "provider": "epusdt"})
		if rec.Code == http.StatusTooManyRequests {
			break
		}
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit: got %d after 11 attempts", rec.Code)
	}

	// 定位批次库故障（令牌查询在 User 表，不中批次定向）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-loc")
	failQueryOn(t, db, "test:cardbatch_q2", isCardBatchModel)
	if rec := doPanel(t, r, "co-loc-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": 1, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("batch locate: %d %s", rec.Code, rec.Body)
	}

	// 余量计数失败（批次在售，计数段注错）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-cnt")
	bid := seedSellableBatch(t, db, "co-cnt-b", 100)
	failQueryOn(t, db, "test:cardcode_q", isCardCodeModel)
	if rec := doPanel(t, r, "co-cnt-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("count: %d %s", rec.Code, rec.Body)
	}

	// 活动查询失败（bestCampaign 首段查询注错）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-camp")
	bid = seedSellableBatch(t, db, "co-camp-b", 100)
	failQueryOn(t, db, "test:campaign_q", isCampaignModel)
	if rec := doPanel(t, r, "co-camp-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("campaign: %d %s", rec.Code, rec.Body)
	}

	// 优惠码核销失败：预检过、网关成单，事务内条件 UPDATE 注错 → 整单回滚 500。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-coup")
	bid = seedSellableBatch(t, db, "co-coup-b", 1000)
	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)
	if err := db.Create(&storage.Coupon{Code: "SAVE10", Kind: "cut", Value: 100, Scope: "all",
		StartsAt: &start, EndsAt: &end, Total: 0, PerUser: 1}).Error; err != nil {
		t.Fatal(err)
	}
	failUpdateOn(t, db, "test:coupon_update", func(v any) bool {
		_, ok := v.(*storage.Coupon)
		return ok
	})
	if rec := doPanel(t, r, "co-coup-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt", "coupon_code": "save10"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("coupon burn: %d %s", rec.Code, rec.Body)
	}

	// 订单落库失败（网关已建单，本地事务注错回滚）。
	r, db = newTestRouterWithDB(t)
	seedPanelUser(t, db, "co-tx")
	bid = seedSellableBatch(t, db, "co-tx-b", 100)
	failCreateOn(t, db, "test:payorder_create", isPayOrderModel)
	if rec := doPanel(t, r, "co-tx-sub", http.MethodPost, "/api/panel/orders",
		map[string]any{"batch_id": bid, "provider": "epusdt"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("order create: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelOrderStatusErrors 订单详情：身份缺失 404、定位库故障 500 与发
// 放查询失败 500。
func TestPanelOrderStatusErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doPanel(t, r, "", http.MethodGet, "/api/panel/orders/whatever", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}

	// 定位库故障（令牌查询之后的订单 First 注错）。
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "os-loc")
	failQueryOn(t, db, "test:payorder_q3", isPayOrderModel)
	if rec := doPanel(t, r, "os-loc-sub", http.MethodGet, "/api/panel/orders/whatever", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	// 发放查询失败。
	r, db = newTestRouterWithDB(t)
	uid := seedPanelUser(t, db, "os-g")
	if err := db.Create(&storage.PaymentOrder{OrderNo: "ORD-9", UserID: uid, Provider: "card",
		Product: "p", AmountCents: 100, Status: "paid"}).Error; err != nil {
		t.Fatal(err)
	}
	failQueryOn(t, db, "test:grant_q2", isGrantModel)
	if rec := doPanel(t, r, "os-g-sub", http.MethodGet, "/api/panel/orders/ORD-9", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("grants: %d %s", rec.Code, rec.Body)
	}
}

// TestPanelSavingsError 节省报表库故障（save_stats 首段查询失败）。
func TestPanelSavingsError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedPanelUser(t, db, "sv-u")
	if err := db.Migrator().DropTable(&storage.SaveStat{}); err != nil {
		t.Fatalf("drop save_stats: %v", err)
	}
	if rec := doPanel(t, r, "sv-u-sub", http.MethodGet, "/api/panel/savings", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("savings: %d %s", rec.Code, rec.Body)
	}
}
