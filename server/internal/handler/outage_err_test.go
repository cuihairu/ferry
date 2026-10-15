package handler

import (
	"net/http"
	"testing"
)

// TestOutageSetFail 覆盖断联态标记保存失败 500（SetSetting 走 Save 的
// Update 链，按 settings 保存序号定向注错）。
func TestOutageSetFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failNthSettingSave("t:outage_set", db, 1)
	if rec := doJSON(t, r, http.MethodPut, "/api/outage", map[string]any{"enabled": true}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("set outage: %d %s", rec.Code, rec.Body)
	}
}
