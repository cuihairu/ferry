package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserTrafficMeteringScope pins the single-end metering scope (F-1242):
// an entry-role node's report attributes to the user ledger, while a report
// from a node whose row cannot be resolved is dropped without error -- the
// user ledger only ever carries deltas with a role verdict behind them.
func TestUserTrafficMeteringScope(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "meter-u", "quota_bytes": 1 << 30}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", "meter-u").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "meter-ent", "address": "m.example.com", "port": 443, "protocol": "vless",
		"role": "entry",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node storage.Node
	if err := db.Where("name = ?", "meter-ent").First(&node).Error; err != nil {
		t.Fatal(err)
	}

	h := &Handler{db: db}
	items := []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "meter-u", Rx: 200, Tx: 100}},
	}}
	if err := h.saveUserTraffic(int64(node.ID), items); err != nil {
		t.Fatalf("entry report: %v", err)
	}
	var rows []storage.TrafficLog
	if err := db.Where("user_id = ?", u.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].RxBytes != 200 || rows[0].TxBytes != 100 {
		t.Fatalf("entry attribution rows = %+v", rows)
	}

	// An unresolvable node never bills: the same payload is dropped silently.
	if err := h.saveUserTraffic(99999, items); err != nil {
		t.Fatalf("unresolved node report: %v", err)
	}
	if err := db.Where("user_id = ?", u.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("unresolved node billed: %d rows", len(rows))
	}
}

// TestUserTrafficNoDoubleBilling pins the entry/landing double-billing guard
// (F-1243): the same user's bytes reported by the entry (the metering end)
// land once; a landing-role node reporting afterwards changes nothing -- the
// user ledger keeps exactly one attribution and last_node still points at the
// entry.
func TestUserTrafficNoDoubleBilling(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "dbl-u", "quota_bytes": 1 << 30}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", "dbl-u").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	mkNode := func(name, role string) uint {
		t.Helper()
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"role": role,
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
		var n storage.Node
		if err := db.Where("name = ?", name).First(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entryID := mkNode("dbl-ent", "entry")
	landingID := mkNode("dbl-lnd", "landing")

	h := &Handler{db: db}
	items := []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "dbl-u", Rx: 200, Tx: 100}},
	}}
	// The entry counts the user's bytes.
	if err := h.saveUserTraffic(int64(entryID), items); err != nil {
		t.Fatalf("entry report: %v", err)
	}
	// The landing's copy of the same flow is the tunnel side: dropped.
	if err := h.saveUserTraffic(int64(landingID), items); err != nil {
		t.Fatalf("landing report: %v", err)
	}

	var rows []storage.TrafficLog
	if err := db.Where("user_id = ?", u.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("user billed %d times, want 1: %+v", len(rows), rows)
	}
	var fresh storage.User
	if err := db.First(&fresh, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.LastNodeID == nil || *fresh.LastNodeID != entryID {
		t.Fatalf("last_node = %v, want the entry node %d", fresh.LastNodeID, entryID)
	}
}
