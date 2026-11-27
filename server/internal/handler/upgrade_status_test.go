package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
)

// TestAgentWSUpgradeStatus covers the F-0712 surface: progress frames land as
// node_upgrades rows, an identical retransmission is deduped, an unknown state
// is ignored, and the admin API lists the rows.
func TestAgentWSUpgradeStatus(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "up-1", "address": "up.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "up-1", Version: "v1",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// One frame per state; applied carries the restart-imminent meaning.
	for i, st := range []string{agentproto.UpgradeStateDownloading, agentproto.UpgradeStateApplying, agentproto.UpgradeStateApplied} {
		env, _ := agentproto.NewEnvelope("upg-9-"+st, agentproto.MsgUpgradeStatus, agentproto.UpgradeStatus{
			UpgradeID: "upg-9", Version: "v2", State: st,
		})
		if err := c.WriteJSON(env); err != nil {
			t.Fatalf("write %s: %v", st, err)
		}
		_ = i
	}
	time.Sleep(200 * time.Millisecond)

	// Retransmission of downloading under the same envelope id must dedupe.
	dup, _ := agentproto.NewEnvelope("upg-9-downloading", agentproto.MsgUpgradeStatus, agentproto.UpgradeStatus{
		UpgradeID: "upg-9", Version: "v2", State: agentproto.UpgradeStateDownloading,
	})
	if err := c.WriteJSON(dup); err != nil {
		t.Fatalf("write dup: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	rec = doJSON(t, r, "GET", "/api/nodes/1/upgrades", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list upgrades: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"state":"applied"`) {
		t.Fatalf("applied must be listed: %s", rec.Body.String())
	}
	if strings.Count(rec.Body.String(), `"upgrade_id":"upg-9"`) != 3 {
		t.Fatalf("expected exactly 3 rows (dedupe on retransmit): %s", rec.Body.String())
	}

	// An unknown state is dropped at the door: no new row, no side effect.
	bad, _ := agentproto.NewEnvelope("upg-9-ghost", agentproto.MsgUpgradeStatus, agentproto.UpgradeStatus{
		UpgradeID: "upg-9", Version: "v2", State: "teleporting",
	})
	if err := c.WriteJSON(bad); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	rec = doJSON(t, r, "GET", "/api/nodes/1/upgrades", nil)
	if strings.Count(rec.Body.String(), `"upgrade_id":"upg-9"`) != 3 {
		t.Fatalf("unknown state must be ignored: %s", rec.Body.String())
	}

	// The failed frame carries its reason through to the record.
	failEnv, _ := agentproto.NewEnvelope("upg-9-failed", agentproto.MsgUpgradeStatus, agentproto.UpgradeStatus{
		UpgradeID: "upg-9", Version: "v2", State: agentproto.UpgradeStateFailed, Error: "download: status 404",
	})
	if err := c.WriteJSON(failEnv); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	rec = doJSON(t, r, "GET", "/api/nodes/1/upgrades", nil)
	if !strings.Contains(rec.Body.String(), `"error":"download: status 404"`) {
		t.Fatalf("failed reason must be stored: %s", rec.Body.String())
	}

	// F-1827: the failed attempt lands exactly one admin-facing herald
	// warning (deduped per upgrade attempt); the earlier progress frames of
	// the same attempt stay quiet.
	var ev storage.Event
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := db.Where("kind = ?", herald.KindUpgradeFailed).First(&ev).Error
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upgrade_failed event missing: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}
	if ev.DedupKey != "node:1:upgrade_failed:upg-9" {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}
	if !strings.Contains(ev.Body, "download: status 404") {
		t.Fatalf("event body must carry the reason: %q", ev.Body)
	}
}
