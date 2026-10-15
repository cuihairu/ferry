package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// rlRaw 规则库推送用原始体请求（octet-stream，带管理员 JWT）。
func rlRaw(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRuleLibPushDBErrors 覆盖规则库推送面：快照建档失败 500（推送链在
// 建档之后，注错即断）、版本史取数失败 500。
func TestRuleLibPushDBErrors(t *testing.T) {
	// 快照建档失败 → 500。
	r, db := newTestRouterWithDB(t)
	_, _ = createNodeFor(t, r, "rl-db")
	db.Callback().Create().Before("gorm:create").Register("t:rlcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.NodeConfig); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := rlRaw(t, r, "POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat", "dat"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}

	// 版本史取数失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:rllist", db2, (*[]storage.NodeConfig)(nil))
	if rec := doJSON(t, r2, "GET", "/api/nodes/1/rulelib", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list fail: %d %s", rec.Code, rec.Body)
	}
}

// TestRuleLibPushTimeout 覆盖 agent 在线但不回执 → 504（hub 60s 等待超时
// 分支），快照置 failed。等待时长取自 rulelibAckTimeout 常量，本用例固
// 有耗时约 60s。
func TestRuleLibPushTimeout(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "rl-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "rl-to", token) // 握手后不应答

	rec := rlRaw(t, r, "POST", fmt.Sprintf("/api/nodes/%d/rulelib?proc=xray&name=geoip.dat", id), "dat")
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
	var row storage.NodeConfig
	if err := db.Where("node_id=? AND kind=?", id, "rulelib:geoip.dat").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "failed" {
		t.Fatalf("snapshot status = %s, want failed", row.Status)
	}
}

// TestRoutingAdsSaveError 覆盖拦截开关保存失败 500（SetSetting 走 Save
// 的 Update 链）。
func TestRoutingAdsSaveError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	db.Callback().Update().Before("gorm:update").Register("t:adssave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, "PUT", "/api/routing/ads", map[string]any{"enabled": true}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("ads save: %d %s", rec.Code, rec.Body)
	}
}
