package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 二十五批补口：代理中心各 400/404/500 口径——登录限速锁定与库故障、建号
// 坏绑定与超长密码（bcrypt 拒收）、停用补写与落库失败、更新各校验与回落
// 再查失败、流水 limit 夹取与两段查询各自失败、账目操作绑定/汇总/落账失败。
// 留白：签发 JWT 的 HS256 签名分支——字符串密钥恒定可签，错误路径不可达不硬凑。

// isDistModel 判断语句是否落在代理表上（Find 链 Model 是切片指针）。
func isDistModel(v any) bool {
	switch v.(type) {
	case *storage.Distributor, []storage.Distributor, *[]storage.Distributor:
		return true
	}
	return false
}

// isDistLedgerModel 判断语句是否落在代理账目流水表上。
func isDistLedgerModel(v any) bool {
	switch v.(type) {
	case *storage.DistributorLedger, []storage.DistributorLedger, *[]storage.DistributorLedger:
		return true
	}
	return false
}

// failDistQueriesAt 第 n 次（1 起）针对代理表的查询失败：更新后回落再查用
// 次序定向。
func failDistQueriesAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:dist_query_at", func(tx *gorm.DB) {
		if !isDistModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistCreates 让代理行创建失败（唯一性外的库故障）。
func failDistCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:dist_create", func(tx *gorm.DB) {
		if isDistModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistUpdates 让代理行更新失败（停用补写/部分更新）。
func failDistUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:dist_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Distributor); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistLedgerQueries 让流水表 Find 失败（对账列表取数）。
func failDistLedgerQueries(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Query().Before("gorm:query").Register("test:distledger_query", func(tx *gorm.DB) {
		if isDistLedgerModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDistLedgerAt 第 n 次（1 起）针对流水表的语句失败：同一样例里 Find 走
// gorm:query 链、汇总 Scan 走 gorm:row 链，两条链挂同一个计数器才能按次序定向。
func failDistLedgerAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	hit := func(tx *gorm.DB) {
		if !isDistLedgerModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	}
	db.Callback().Query().Before("gorm:query").Register("test:distledger_q_at", hit)
	db.Callback().Row().Before("gorm:row").Register("test:distledger_r_at", hit)
}

// failDistLedgerCreates 让流水行落账失败。
func failDistLedgerCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:distledger_create", func(tx *gorm.DB) {
		if isDistLedgerModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedDist 经管理面建代理号并返回行 id。
func seedDist(t *testing.T, r *gin.Engine, db *gorm.DB, name string) uint {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": name, "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dist %s: %d %s", name, rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.Where("username = ?", name).First(&d).Error; err != nil {
		t.Fatalf("find dist %s: %v", name, err)
	}
	return d.ID
}

// TestDistributorLoginErrors 登录：限速锁定、坏绑定与库故障。
func TestDistributorLoginErrors(t *testing.T) {
	// 连续失败达阈即锁定：第五次失败后落锁，第六次请求直接 429。
	r, db := newTestRouterWithDB(t)
	seedDist(t, r, db, "login-l")
	for i := 0; i < 5; i++ {
		if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
			map[string]any{"username": "login-l", "password": "wrong"}); rec.Code != http.StatusUnauthorized {
			t.Fatalf("fail %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
		map[string]any{"username": "login-l", "password": "secret123"}); rec.Code != http.StatusTooManyRequests ||
		!strings.Contains(rec.Body.String(), "尝试过于频繁") {
		t.Fatalf("locked: %d %s", rec.Code, rec.Body)
	}

	// 坏绑定 400。
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// 库故障 500（内部错误，不外泄）。
	r, db = newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/distributor/login",
		map[string]any{"username": "x", "password": "y"}); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "内部错误") {
		t.Fatalf("db: %d %s", rec.Code, rec.Body)
	}
}

// TestListDistributorsErrors 列表：代理取数失败与汇总失败各自冒 500。
func TestListDistributorsErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "list-l")
	failDistLedgerAt(t, db, 1) // 流水表首条语句即汇总（Query 链无前置语句）
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDistributorErrors 建号：坏绑定、bcrypt 超长密码、停用补写失败与
// 落库失败。
func TestCreateDistributorErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// bcrypt 超过 72 字节即拒收，冒 500。
	r, _ = newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors", map[string]any{
		"username": "longpw", "password": strings.Repeat("p", 100),
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// 备注入库 + 显式停用补写（Enabled 有 default:true，零值会被略过）。
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, http.MethodPost, "/api/distributors", map[string]any{
		"username": "note-l", "password": "secret123",
		"note": "  华东渠道 ", "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.Where("username = ?", "note-l").First(&d).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if d.Enabled || d.Note != "华东渠道" {
		t.Fatalf("not persisted: %+v", d)
	}

	// 停用补写失败 500。
	r, db = newTestRouterWithDB(t)
	failDistUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": "wb-fail", "password": "secret123", "enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// 落库失败（非唯一冲突）冒 500。
	r, db = newTestRouterWithDB(t)
	failDistCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors",
		map[string]any{"username": "create-fail", "password": "secret123"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDistributorErrors 更新：定位校验、密码重置与校验失败、各写库
// 故障与回落再查失败。
func TestUpdateDistributorErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/abc", map[string]any{"enabled": false}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-l")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", map[string]any{"enabled": false}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	// 校验面：折扣越界与无可更新字段。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-val")
	for _, tc := range []struct {
		name string
		body any
	}{
		{"discount", map[string]any{"discount_percent": 150}},
		{"empty", map[string]any{}},
	} {
		if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1", tc.body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	// 密码重置成功（哈希重写不外泄明文）。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-pw")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"password": "newsecret1"}); rec.Code != http.StatusOK {
		t.Fatalf("password set: %d %s", rec.Code, rec.Body)
	}
	var d storage.Distributor
	if err := db.First(&d, 1).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if d.PasswordHash == "" || d.PasswordHash == "newsecret1" {
		t.Fatalf("hash not rewritten: %q", d.PasswordHash)
	}

	// 超长密码 500。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-pwbad")
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"password": strings.Repeat("p", 100)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("long password: %d %s", rec.Code, rec.Body)
	}

	// 更新语句失败 500。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-write")
	failDistUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"discount_percent": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}

	// 回落再查失败 500（定位 1 → 更新 → 复查）。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "upd-refind")
	failDistQueriesAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodPut, "/api/distributors/1",
		map[string]any{"discount_percent": 10}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}
}

// TestDistributorLedgerErrors 流水：定位校验、limit 夹取、取数与汇总各自失败。
func TestDistributorLedgerErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/abc/ledger", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	// limit 口径：解析失败回落缺省 50、合法值生效、越界夹取到 1 与 200。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-l")
	for _, limit := range []string{"abc", "10", "0", "500"} {
		if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger?limit="+limit, nil); rec.Code != http.StatusOK {
			t.Fatalf("limit=%s: %d %s", limit, rec.Code, rec.Body)
		}
	}

	// 取数失败 500。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-find")
	failDistLedgerQueries(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows: %d %s", rec.Code, rec.Body)
	}

	// 汇总失败 500（取数 1 走 Query 链，汇总 2 走 Row 链）。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "led-totals")
	failDistLedgerAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodGet, "/api/distributors/1/ledger", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}
}

// TestWriteLedgerOpErrors 账目操作：定位校验、坏绑定、汇总失败与落账失败。
func TestWriteLedgerOpErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, kind := range []string{"payout", "adjust"} {
		if rec := doJSON(t, r, http.MethodPost, "/api/distributors/abc/"+kind,
			map[string]any{"amount_cents": 100}); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s bad id: %d %s", kind, rec.Code, rec.Body)
		}
	}

	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/payout",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-bind")
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// 打款非正金额拒收（人工补偿走 adjust，不打零款）。
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/payout",
		map[string]any{"amount_cents": 0}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "amount_cents must be") {
		t.Fatalf("payout zero: %d %s", rec.Code, rec.Body)
	}

	// 汇总失败 500（校验已过、取数前置无流水语句）。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-totals")
	failDistLedgerAt(t, db, 1)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("totals: %d %s", rec.Code, rec.Body)
	}

	// 落账失败 500（adjust 不受余额约束，先过校验再落 Create）。
	r, db = newTestRouterWithDB(t)
	seedDist(t, r, db, "op-create")
	failDistLedgerCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/distributors/1/adjust",
		map[string]any{"amount_cents": 100}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
}
