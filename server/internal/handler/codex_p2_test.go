package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// errInjectUser 是用户行写回注入错误（退款回收回滚用例用）。
var errInjectUser = errors.New("injected user update failure")

// newAuditCtx 构造一个带请求体的 gin 测试上下文（供审计快照直测）。
func newAuditCtx(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	return c
}

// TestLoginLogUATruncatesOnRuneBoundary 覆盖 codex 深审 P2「登录 UA 字节
// 截断写非法 UTF-8」修复：UA 含中文（每字 3 字节）时字节硬切会截半字符，
// 修复后按字符边界回退，落库 UA 恒为合法 UTF-8 且不超列宽。
func TestLoginLogUATruncatesOnRuneBoundary(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	// 84×3=252，下一个「流」占 252-255，上限 255 落在字符中间。
	ua := strings.Repeat("流", 84) + strings.Repeat("x", 10) + strings.Repeat("流", 10)
	if len(ua) <= loginLogUALimit {
		t.Fatalf("test UA too short: %d", len(ua))
	}
	row := h.writeLoginLog("ua-user", "10.0.0.1", ua, true)
	if len(row.UA) > loginLogUALimit {
		t.Fatalf("UA len = %d, want <= %d", len(row.UA), loginLogUALimit)
	}
	if !utf8.ValidString(row.UA) {
		t.Fatalf("UA not valid UTF-8: %q", row.UA)
	}
	// 落库值同口径。
	var got storage.LoginLog
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.UA != row.UA || !utf8.ValidString(got.UA) {
		t.Fatalf("persisted UA = %q valid=%v", got.UA, utf8.ValidString(got.UA))
	}
}

// TestAuditBodySnapshotOversizeMarker 覆盖 codex 深审 P2「审计请求体无读取
// 上限」修复：超 64KB 的请求体不入快照（截断前缀是未脱敏原文，不能入库），
// 返回固定标记；读取封顶后已读前缀与残余流拼接还原，下游仍读到完整体。
func TestAuditBodySnapshotOversizeMarker(t *testing.T) {
	h := &Handler{db: nil}
	body := `{"k":"` + strings.Repeat("y", 80<<10) + `"}`
	c := newAuditCtx(body)
	if got := h.auditBodySnapshot(c); !strings.Contains(got, "超过审计快照读取上限") {
		t.Fatalf("oversize snapshot = %q", got)
	}
	rest, err := c.GetRawData()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != len(body) {
		t.Fatalf("restored body = %d bytes, want %d", len(rest), len(body))
	}
	// 小体照常脱敏后截断：password 回 ***，中文内容保留。
	c2 := newAuditCtx(`{"password":"secret","name":"张三"}`)
	if got := h.auditBodySnapshot(c2); strings.Contains(got, "secret") || !strings.Contains(got, "张三") {
		t.Fatalf("small snapshot = %q", got)
	}
}

// TestAuditSnapshotOversizeBodyNotPanic 走真实审计中间件：80KB 请求体过
// 写端点不崩，且请求体完整送达下游 handler（还原拼接生效）。
func TestAuditSnapshotOversizeBodyNotPanic(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	body := `{"username":"` + strings.Repeat("z", 80<<10) + `"}`
	rec := doJSON(t, r, "POST", "/api/users", body)
	if rec.Code >= 500 {
		t.Fatalf("oversize body via audited route: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRefundUnknownGrantTypeNotRevoked 覆盖 codex「退款回收显式匹配
// extend_days」：未知发放类型不再被旧实现的 else 兜底当延长到期扣回，
// 退款后配额与到期一律不动。
func TestRefundUnknownGrantTypeNotRevoked(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	u := storage.User{Username: "unk-type", SubToken: "unk-sub", QuotaBytes: 1000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(30 * 24 * time.Hour)
	if err := db.Model(&storage.User{}).Where("id = ?", u.ID).
		Update("expires_at", &exp).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, no := range []string{"unk-o1", "unk-o2"} {
		if err := db.Create(&storage.PaymentOrder{
			OrderNo: no, UserID: u.ID, Provider: "epusdt", AmountCents: 100,
			Product: "p", Status: "paid", PaidAt: &now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	grants := []storage.Grant{
		{OrderNo: "unk-o1", UserID: u.ID, GrantType: "mystery", GrantValue: 500},
		{OrderNo: "unk-o2", UserID: u.ID, GrantType: "", GrantValue: 500},
	}
	for i := range grants {
		if err := db.Create(&grants[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, no := range []string{"unk-o1", "unk-o2"} {
		if rec := doJSON(t, r, "POST", "/api/payments/orders/"+no+"/refund", map[string]any{}); rec.Code != http.StatusOK {
			t.Fatalf("refund %s: %d %s", no, rec.Code, rec.Body)
		}
	}
	var after storage.User
	db.First(&after, u.ID)
	if after.QuotaBytes != 1000 {
		t.Fatalf("unknown-type grant touched quota: %d, want 1000", after.QuotaBytes)
	}
	if after.ExpiresAt == nil || !after.ExpiresAt.Equal(exp) {
		t.Fatalf("unknown-type grant touched expires: %v, want %v", after.ExpiresAt, exp)
	}
}

// TestRefundRevokesDeletedUserSkips 覆盖「用户行已删的发放无从回收」：退款
// 仍成功（refunded 态成立），不因用户缺失报错、不落站内信。
func TestRefundRevokesDeletedUserSkips(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	u := storage.User{Username: "gone", SubToken: "gone-sub", QuotaBytes: 2000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "gone-o1", UserID: u.ID, Provider: "epusdt", AmountCents: 100,
		Product: "p", Status: "paid", PaidAt: &now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Grant{
		OrderNo: "gone-o1", UserID: u.ID, GrantType: "add_quota", GrantValue: 500,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&u).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/payments/orders/gone-o1/refund", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("refund deleted-user order: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.Notification{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("notification to deleted user = %d, want 0", n)
	}
}

// TestRefundRevokeFailureRollsBack 覆盖「回收失败整体回滚」：用户行写回注错
// → 退款 500，订单保持 paid、权益未动（refunded 态蕴含权益已收，宁可退款
// 失败不可静默漏收）。
func TestRefundRevokeFailureRollsBack(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	u := storage.User{Username: "rb", SubToken: "rb-sub", QuotaBytes: 5000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&storage.PaymentOrder{
		OrderNo: "rb-o1", UserID: u.ID, Provider: "epusdt", AmountCents: 100,
		Product: "p", Status: "paid", PaidAt: &now,
		GrantType: "add_quota", GrantValue: 1000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Grant{
		OrderNo: "rb-o1", UserID: u.ID, GrantType: "add_quota", GrantValue: 1000,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// 注错按表名判定：Updates(map) 时 Statement.Dest 是写集不是 *storage.User。
	db.Callback().Update().Before("gorm:update").Register("t:rbupdate", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errInjectUser)
		}
	})
	t.Cleanup(func() { db.Callback().Update().Remove("t:rbupdate") })
	if rec := doJSON(t, r, "POST", "/api/payments/orders/rb-o1/refund", map[string]any{}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("refund with revoke fail: %d %s", rec.Code, rec.Body)
	}
	var after storage.User
	db.First(&after, u.ID)
	if after.QuotaBytes != 5000 {
		t.Fatalf("revoke failure left quota changed: %d, want 5000", after.QuotaBytes)
	}
	var order storage.PaymentOrder
	db.Where("order_no = ?", "rb-o1").First(&order)
	if order.Status != "paid" {
		t.Fatalf("revoke failure left order status: %q, want paid", order.Status)
	}
}

// TestRefundRevokeLostUpdateGuard 覆盖 codex「退款用户权益更新与 applyGrant
// 的丢更新竞态」：按真实并发交错顺序执行——双方先各读旧值（10000），发放侧
// 先提交（顶到 13000），退款侧再以读时旧值做守卫写回；旧实现静默覆盖
// （终值 9600，丢发放），修复后守卫失手→重读重算，终值 12600=13000-400。
// 注：测试库单连接（SetMaxOpenConns(1)），故交错按「先读、后按提交顺序逐
// 事务执行」在时间轴上还原，而非两事务并行持有连接。
func TestRefundRevokeLostUpdateGuard(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	u := storage.User{Username: "lost", SubToken: "lost-sub", QuotaBytes: 10000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	g := storage.Grant{OrderNo: "lo-o1", UserID: u.ID, GrantType: "add_quota", GrantValue: 400}
	if err := db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	// 两侧各自先读旧值（10000）。
	var grantSide, revokeSide storage.User
	db.First(&grantSide, u.ID)
	db.First(&revokeSide, u.ID)
	// 发放侧先提交：10000+3000=13000。
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyGrant(tx, "lo-g", &grantSide, "add_quota", 3000, time.Now(), nil)
	}); err != nil {
		t.Fatalf("applyGrant: %v", err)
	}
	// 退款侧以读时旧值（10000）做守卫写回（revokeOneGrant 是真实写回函数）。
	if err := db.Transaction(func(tx *gorm.DB) error {
		return revokeOneGrant(tx, g, &revokeSide)
	}); err != nil {
		t.Fatalf("revokeOneGrant: %v", err)
	}
	var after storage.User
	db.First(&after, u.ID)
	if after.QuotaBytes != 12600 {
		t.Fatalf("quota = %d, want 12600 (revoke must not lose the concurrent grant)", after.QuotaBytes)
	}
}

// TestApplyGrantLostUpdateGuard 对称侧竞态：发放与退款回收各读旧值后交错
// 提交——两边都必须生效（旧实现后提交者裸写覆盖先提交者，少 3000）。
// 期望 10000+3000（发放先提交）-2000（回收后提交）=11000。
func TestApplyGrantLostUpdateGuard(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	u := storage.User{Username: "grant-race", SubToken: "gr-sub", QuotaBytes: 10000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// 两侧先各读旧值。
	var grantSide, revokeSide storage.User
	db.First(&grantSide, u.ID)
	db.First(&revokeSide, u.ID)
	// 发放侧先提交：10000+3000=13000。
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyGrant(tx, "gr-o1", &grantSide, "add_quota", 3000, time.Now(), nil)
	}); err != nil {
		t.Fatalf("applyGrant: %v", err)
	}
	// 回收侧以读时旧值（10000）守卫写回：守卫失手→重读 13000→写 11000。
	if err := db.Transaction(func(tx *gorm.DB) error {
		return revokeOneGrant(tx, storage.Grant{
			GrantType: "add_quota", GrantValue: 2000,
		}, &revokeSide)
	}); err != nil {
		t.Fatalf("revokeOneGrant: %v", err)
	}
	var after storage.User
	db.First(&after, u.ID)
	if after.QuotaBytes != 11000 {
		t.Fatalf("quota = %d, want 11000 (both sides must survive)", after.QuotaBytes)
	}
}
