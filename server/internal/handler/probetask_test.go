package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// createProbeNode registers a node and returns its id and token.
func createProbeNode(t *testing.T, r *gin.Engine) (float64, string) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "probe-node", "address": "sh.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	return node["id"].(float64), node["token"].(string)
}

// TestSetProbeTasks covers PUT /api/nodes/:id/probe_tasks (F-0921): a valid
// set persists as the node's desired dispatch, an unset interval defaults to
// 60s, an invalid set is rejected before touching the row, and an empty set
// withdraws everything.
func TestSetProbeTasks(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	// offline node: the set persists, delivered stays false
	rec := doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{
		"specs": []map[string]any{{
			"name": "entry-cross", "target_kind": "peer", "target": "192.0.2.10:443",
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set probe tasks: %d %s", rec.Code, rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["delivered"] != false || out["applied"].(float64) != 1 {
		t.Fatalf("response = %s; want delivered=false applied=1", rec.Body)
	}
	if v, _ := out["version"].(string); v == "" {
		t.Fatal("response must carry the content version")
	}

	var n storage.Node
	if err := db.First(&n, 1).Error; err != nil {
		t.Fatalf("load node: %v", err)
	}
	if n.ProbeTasks == "" {
		t.Fatal("probe_tasks must be persisted")
	}
	var saved agentproto.ProbeTask
	if err := json.Unmarshal([]byte(n.ProbeTasks), &saved); err != nil {
		t.Fatalf("stored probe_tasks: %v", err)
	}
	if len(saved.Specs) != 1 || saved.Specs[0].IntervalSec != 60 {
		t.Fatalf("stored specs = %+v; want interval defaulted to 60", saved.Specs)
	}

	// invalid target kind: rejected, the row keeps the old set
	rec = doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{
		"specs": []map[string]any{{"name": "bad", "target_kind": "node", "target": "x:1", "interval_sec": 30}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, 1).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	var kept agentproto.ProbeTask
	if err := json.Unmarshal([]byte(n.ProbeTasks), &kept); err != nil || len(kept.Specs) != 1 || kept.Specs[0].Name != "entry-cross" {
		t.Fatalf("rejected dispatch must not touch the row: %s (%v)", n.ProbeTasks, err)
	}

	// withdrawal: an empty spec list clears the desired set
	rec = doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{"specs": []map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("withdraw: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, 1).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if n.ProbeTasks != "" {
		t.Fatalf("probe_tasks after withdraw = %q; want empty", n.ProbeTasks)
	}
}

// TestSetProbeTasksValidation covers the mirrored agent rules: duplicate names,
// a bad interval and transport-specific violations are rejected with 400.
func TestSetProbeTasksValidation(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	cases := []struct {
		name  string
		specs []map[string]any
		want  string
	}{
		{"duplicate names", []map[string]any{
			{"name": "dup", "target_kind": "peer", "target": "a:1", "interval_sec": 30},
			{"name": "dup", "target_kind": "peer", "target": "a:1", "interval_sec": 30},
		}, "duplicate probe name"},
		{"bad interval", []map[string]any{
			{"name": "fast", "target_kind": "peer", "target": "a:1", "interval_sec": 2},
		}, "interval_sec"},
		{"udp without payload", []map[string]any{
			{"name": "udpnopayload", "target_kind": "exit", "target": "a:53", "proto": "udp", "interval_sec": 30},
		}, "payload_hex"},
		{"http_path without slash", []map[string]any{
			{"name": "path", "target_kind": "exit", "target": "a:80", "proto": "http", "http_path": "x", "interval_sec": 30},
		}, "http_path"},
		{"expect_status out of range", []map[string]any{
			{"name": "status", "target_kind": "exit", "target": "a:80", "proto": "http", "expect_status": 42, "interval_sec": 30},
		}, "expect_status"},
	}
	for _, tc := range cases {
		rec := doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{"specs": tc.specs})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s; want 400 with %q", tc.name, rec.Code, rec.Body, tc.want)
		}
	}
}

// TestSetProbeTasksMissingNode covers a dispatch to an unknown node: 404.
func TestSetProbeTasksMissingNode(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "PUT", "/api/nodes/999/probe_tasks", map[string]any{"specs": []map[string]any{}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node: %d %s", rec.Code, rec.Body)
	}
}

// TestProbeTaskHelloDispatch covers the full F-0921 delivery over a live
// connection: the persisted desired set arrives right after hello_ack (catch-up
// for nodes that were offline), and a subsequent PUT delivers to the online
// agent and reports delivered=true once the ack comes back.
func TestProbeTaskHelloDispatch(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	// desired set persisted while the node is offline
	rec := doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{
		"specs": []map[string]any{{
			"name": "entry-cross", "target_kind": "peer", "target": "192.0.2.10:443", "interval_sec": 30,
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set probe tasks: %d %s", rec.Code, rec.Body)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	var token string
	if err := db.Table("nodes").Select("token").Where("id=1").Scan(&token).Error; err != nil || token == "" {
		t.Fatalf("load token: %v (%q)", err, token)
	}
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "probe-node", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// background agent: answer every probe_task with an OK ack
	acked := make(chan agentproto.ProbeTask, 4)
	go func() {
		for {
			_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
			_, raw, err := c.ReadMessage()
			if err != nil {
				return
			}
			var env agentproto.Envelope
			if json.Unmarshal(raw, &env) != nil || env.Type != agentproto.MsgProbeTask {
				continue
			}
			var task agentproto.ProbeTask
			if env.Decode(&task) != nil {
				continue
			}
			acked <- task
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProbeTaskAck, agentproto.ProbeTaskAck{
				Version: task.Version, OK: true, Applied: len(task.Specs),
			})
			if c.WriteJSON(reply) != nil {
				return
			}
		}
	}()

	// catch-up: the persisted set arrives on its own right after the handshake
	select {
	case task := <-acked:
		if len(task.Specs) != 1 || task.Specs[0].Name != "entry-cross" || task.Version == "" {
			t.Fatalf("catch-up dispatch = %+v", task)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no catch-up probe_task after hello")
	}

	// online delivery: a fresh PUT goes out immediately and reports delivered=true
	rec = doJSON(t, r, "PUT", "/api/nodes/1/probe_tasks", map[string]any{
		"specs": []map[string]any{{
			"name": "egress-watch", "target_kind": "exit", "target": "www.gstatic.com:443",
			"proto": "https", "http_path": "/generate_204", "expect_status": 204, "interval_sec": 60,
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("online dispatch: %d %s", rec.Code, rec.Body)
	}
	var out2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out2)
	if out2["delivered"] != true {
		t.Fatalf("online dispatch response = %s; want delivered=true", rec.Body)
	}
	select {
	case task := <-acked:
		if len(task.Specs) != 1 || task.Specs[0].Name != "egress-watch" {
			t.Fatalf("online dispatch pushed = %+v", task)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no probe_task push for the online dispatch")
	}
}
