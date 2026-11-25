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

// errInjectUser is the injected error for writing the user row back (used by the
// refund revoke rollback case).
var errInjectUser = errors.New("injected user update failure")

// newAuditCtx builds a gin test context carrying a request body (for driving the
// audit snapshot directly).
func newAuditCtx(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	return c
}

// TestLoginLogUATruncatesOnRuneBoundary covers the codex deep-review P2 fix for
// "login UA byte truncation writes invalid UTF-8": when the UA contains CJK
// text (3 bytes per rune), a hard byte cut splits a rune in half; after the
// fix the truncation backtracks to a rune boundary, so the persisted UA is always
// valid UTF-8 and never exceeds the column width.
func TestLoginLogUATruncatesOnRuneBoundary(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	// 84*3=252, so the first CJK rune of the repeat below occupies 252-255 and the 255 cap lands mid-rune.
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
	// The persisted value follows the same rule.
	var got storage.LoginLog
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.UA != row.UA || !utf8.ValidString(got.UA) {
		t.Fatalf("persisted UA = %q valid=%v", got.UA, utf8.ValidString(got.UA))
	}
}

// TestAuditBodySnapshotOversizeMarker covers the codex deep-review P2 fix for
// "unbounded audit request body reads": bodies over 64KB are not snapshotted
// (the truncated prefix is raw, unsanitized text and must not be persisted); a
// fixed marker is returned instead. After the read cap the consumed prefix is
// spliced back with the remaining stream, so downstream still reads the full
// body.
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
	// Small bodies are sanitized as usual before truncation: password becomes ***,
	// and CJK content is kept.
	c2 := newAuditCtx(`{"password":"secret","name":"张三"}`)
	if got := h.auditBodySnapshot(c2); strings.Contains(got, "secret") || !strings.Contains(got, "张三") {
		t.Fatalf("small snapshot = %q", got)
	}
}

// TestAuditSnapshotOversizeBodyNotPanic drives the real audit middleware: an
// 80KB request body through a write endpoint does not panic, and the body still
// reaches the downstream handler intact (the splice-back restore works).
func TestAuditSnapshotOversizeBodyNotPanic(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	body := `{"username":"` + strings.Repeat("z", 80<<10) + `"}`
	rec := doJSON(t, r, "POST", "/api/users", body)
	if rec.Code >= 500 {
		t.Fatalf("oversize body via audited route: %d %s", rec.Code, rec.Body.String())
	}
}

// TestRefundUnknownGrantTypeNotRevoked covers codex "refund revoke matches
// extend_days explicitly": an unknown grant type is no longer treated as an
// expiration extension and clawed back by the old implementation's else
// fallback, so refund leaves both quota and expiry untouched.
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

// TestRefundRevokesDeletedUserSkips covers "a grant with no user row left to
// revoke": the refund still succeeds (the refunded state holds) without erroring
// on the missing user or writing an in-app notification.
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

// TestRefundRevokeFailureRollsBack covers "a failed revoke rolls everything
// back": injecting an error into the user row write-back makes the refund return
// 500, with the order still paid and the entitlements untouched (the refunded
// state implies the entitlement was reclaimed, so failing the refund beats
// silently missing the clawback).
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
	// Injection is keyed on the table name: with Updates(map) Statement.Dest is the
	// write set, not *storage.User.
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

// TestRefundRevokeLostUpdateGuard covers codex "lost-update race between the
// refund user entitlement update and applyGrant": the real concurrent
// interleaving is reproduced — both sides first read the old value (10000),
// the grant side commits first (pushing it to 13000), then the refund side does
// a guarded write-back using the stale value it read; the old implementation
// overwrote it silently (final 9600, losing the grant), while after the fix a
// failed guard triggers a re-read and recompute, giving 12600=13000-400.
// Note: the test DB has a single connection (SetMaxOpenConns(1)), so the
// interleaving is reproduced on the timeline as "read first, then run each
// transaction in commit order" rather than two transactions holding connections
// in parallel.
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
	// Both sides read the old value (10000) first.
	var grantSide, revokeSide storage.User
	db.First(&grantSide, u.ID)
	db.First(&revokeSide, u.ID)
	// Grant side commits first: 10000+3000=13000.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyGrant(tx, "lo-g", &grantSide, "add_quota", 3000, time.Now(), nil)
	}); err != nil {
		t.Fatalf("applyGrant: %v", err)
	}
	// Refund side writes back with the stale value read at that time (10000) under
	// the guard (revokeOneGrant is the real write-back function).
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

// TestApplyGrantLostUpdateGuard is the symmetric side of the race: grant and
// refund revoke each read the old value, then commit interleaved — both sides
// must take effect (the old implementation let the later committer's bare write
// clobber the earlier one, losing 3000).
// Expected 10000+3000 (grant commits first) -2000 (revoke commits later) =11000.
func TestApplyGrantLostUpdateGuard(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	u := storage.User{Username: "grant-race", SubToken: "gr-sub", QuotaBytes: 10000, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// Both sides read the old value first.
	var grantSide, revokeSide storage.User
	db.First(&grantSide, u.ID)
	db.First(&revokeSide, u.ID)
	// Grant side commits first: 10000+3000=13000.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return applyGrant(tx, "gr-o1", &grantSide, "add_quota", 3000, time.Now(), nil)
	}); err != nil {
		t.Fatalf("applyGrant: %v", err)
	}
	// Revoke side writes back under the guard using the stale value read at that
	// time (10000): failed guard → re-read 13000 → write 11000.
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
