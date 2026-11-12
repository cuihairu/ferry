package handler

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserRenameKeepsTraffic 覆盖 P2 用户改名流量漏计修复：xray 流量按
// email（== users.username 约定）归属，改名后节点侧配置未重推期间旧
// email 仍在上报——旧名留作别名，saveUserTraffic 仍按别名归属到原用户，
// 不静默丢弃。修复前改名即断映射，旧名上报全部漏计。
func TestUserRenameKeepsTraffic(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "alice", "quota_bytes": 1 << 30}); rec.Code != http.StatusCreated {
		t.Fatalf("create alice: %d %s", rec.Code, rec.Body)
	}
	var alice storage.User
	if err := db.Where("username = ?", "alice").First(&alice).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "rn-1", "address": "us.example.com", "port": 443, "protocol": "vless",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node storage.Node
	if err := db.Where("name = ?", "rn-1").First(&node).Error; err != nil {
		t.Fatal(err)
	}

	// 改名 alice → alice2：旧名 alice 应留别名。
	idStr := strconv.FormatUint(uint64(alice.ID), 10)
	if rec := doJSON(t, r, "PUT", "/api/users/"+idStr, map[string]any{"username": "alice2"}); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	var alias storage.UserAlias
	if err := db.Where("username = ?", "alice").First(&alias).Error; err != nil {
		t.Fatalf("alias not recorded on rename: %v", err)
	}
	if alias.UserID != alice.ID {
		t.Fatalf("alias user_id = %d; want %d", alias.UserID, alice.ID)
	}

	// 旧名 alice 的上报（节点配置未重推）：应按别名归属到 alice，不丢弃。
	h := &Handler{db: db}
	if err := h.saveUserTraffic(int64(node.ID), []agentproto.ProcTraffic{
		{Proc: "xray", Rx: 300, Tx: 150, At: time.Now(),
			Users: []agentproto.UserTraffic{
				{Email: "alice", Rx: 200, Tx: 100}, // 旧名：别名归属
				{Email: "alice2", Rx: 100, Tx: 50}, // 新名：当前用户名归属
			}},
	}); err != nil {
		t.Fatalf("saveUserTraffic: %v", err)
	}

	var rows []storage.TrafficLog
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("traffic rows = %d, want 2 (旧名+新名都不丢)", len(rows))
	}
	// 两行都应归属 alice（改名不换用户）。
	for _, row := range rows {
		if row.UserID == nil || *row.UserID != alice.ID {
			t.Fatalf("row user_id = %v; want %d (旧名漏计未修复)", row.UserID, alice.ID)
		}
	}
}
