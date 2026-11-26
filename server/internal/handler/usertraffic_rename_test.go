package handler

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserRenameKeepsTraffic covers the P2 fix for lost traffic accounting on
// user rename: xray traffic is attributed by email (== users.username
// convention), and until the node-side config is re-pushed after a rename the
// old email keeps reporting -- the old name stays as an alias and
// saveUserTraffic still attributes it to the original user by alias instead of
// silently dropping it. Before the fix a rename broke the mapping and every
// report under the old name was lost.
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
		"role": "entry",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node storage.Node
	if err := db.Where("name = ?", "rn-1").First(&node).Error; err != nil {
		t.Fatal(err)
	}

	// Rename alice -> alice2: the old name alice should remain an alias.
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

	// A report under the old name alice (node config not re-pushed): should be
	// attributed to alice via the alias, not dropped.
	h := &Handler{db: db}
	if err := h.saveUserTraffic(int64(node.ID), []agentproto.ProcTraffic{
		{Proc: "xray", Rx: 300, Tx: 150, At: time.Now(),
			Users: []agentproto.UserTraffic{
				{Email: "alice", Rx: 200, Tx: 100}, // old name: attributed via alias
				{Email: "alice2", Rx: 100, Tx: 50}, // new name: attributed to the current username
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
	// Both rows should belong to alice (renaming does not change the user).
	for _, row := range rows {
		if row.UserID == nil || *row.UserID != alice.ID {
			t.Fatalf("row user_id = %v; want %d (旧名漏计未修复)", row.UserID, alice.ID)
		}
	}
}
