package handler

import (
	"encoding/json"
	"fmt"
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

// createLandingPath registers a landing node and one path over it; both use
// distinct names per call so tests can create several.
func createLandingPath(t *testing.T, r *gin.Engine, suffix string) (entryID, landingID, pathID float64) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "entry-" + suffix, "address": "sh.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create entry: %d %s", rec.Code, rec.Body)
	}
	var entry map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &entry)

	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "landing-" + suffix, "address": "sg.example.com", "port": 8443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create landing: %d %s", rec.Code, rec.Body)
	}
	var landing map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &landing)

	rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name":            "path-" + suffix,
		"entry_node_id":   entry["id"],
		"landing_node_id": landing["id"],
		"direction":       "in",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path: %d %s", rec.Code, rec.Body)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["probe_importance"] != "normal" {
		t.Fatalf("default probe_importance = %v; want normal", p["probe_importance"])
	}
	return entry["id"].(float64), landing["id"].(float64), p["id"].(float64)
}

// TestPathProbeImportanceValidation covers the F-0924 field: it persists, an
// unknown grade is rejected, and updates merge it.
func TestPathProbeImportanceValidation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, _, pathID := createLandingPath(t, r, "imp")

	rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/paths/%d", int(pathID)), map[string]any{
		"probe_importance": "critical",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update importance: %d %s", rec.Code, rec.Body)
	}
	var p storage.Path
	if err := db.First(&p, int(pathID)).Error; err != nil {
		t.Fatalf("load path: %v", err)
	}
	if p.ProbeImportance != "critical" {
		t.Fatalf("stored importance = %q; want critical", p.ProbeImportance)
	}

	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/paths/%d", int(pathID)), map[string]any{
		"probe_importance": "urgent",
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "probe_importance") {
		t.Fatalf("unknown grade: %d %s", rec.Code, rec.Body)
	}
}

// TestSyncProbeTasksFromPaths covers the F-0924 sync: each enabled path lands
// as a path-<id> tunnel task on its entry node at the importance-derived
// interval; disabled paths drop out on the next sync; operator-authored tasks
// survive.
func TestSyncProbeTasksFromPaths(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	entryID, _, criticalID := createLandingPath(t, r, "a")

	// grade the first path critical: its dispatched probe must run every minute
	rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/paths/%d", int(criticalID)), map[string]any{
		"probe_importance": "critical",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("grade critical: %d %s", rec.Code, rec.Body)
	}

	// second path on the same entry with low importance
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "landing-b", "address": "jp.example.com", "port": 443, "protocol": "vless",
	})
	var landing map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &landing)
	rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "path-b", "entry_node_id": entryID, "landing_node_id": landing["id"],
		"probe_importance": "low",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path-b: %d %s", rec.Code, rec.Body)
	}
	var lowPath map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &lowPath)
	lowID := int(lowPath["id"].(float64))

	// an operator-authored task on the entry node must survive the sync
	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/nodes/%d/probe_tasks", int(entryID)), map[string]any{
		"specs": []map[string]any{{
			"name": "egress-watch", "target_kind": "exit", "target": "www.gstatic.com:443",
			"proto": "https", "http_path": "/generate_204", "expect_status": 204, "interval_sec": 60,
		}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("operator task: %d %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, r, "POST", "/api/probe-tasks/sync-from-paths", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", rec.Code, rec.Body)
	}

	var n storage.Node
	if err := db.First(&n, int(entryID)).Error; err != nil {
		t.Fatalf("load entry: %v", err)
	}
	var saved agentproto.ProbeTask
	if err := json.Unmarshal([]byte(n.ProbeTasks), &saved); err != nil {
		t.Fatalf("stored probe_tasks: %v", err)
	}
	byName := map[string]agentproto.ProbeTaskSpec{}
	for _, s := range saved.Specs {
		byName[s.Name] = s
	}
	if len(byName) != 3 {
		t.Fatalf("stored %d tasks (%+v); want 3", len(saved.Specs), saved.Specs)
	}
	crit := byName[fmt.Sprintf("path-%d", int(criticalID))]
	if crit.TargetKind != "tunnel" || crit.Target != "sg.example.com:8443" || crit.Direction != "in" || crit.IntervalSec != 60 {
		t.Fatalf("critical path task = %+v", crit)
	}
	low := byName[fmt.Sprintf("path-%d", lowID)]
	if low.IntervalSec != 900 || low.Target != "jp.example.com:443" {
		t.Fatalf("low path task = %+v", low)
	}
	if byName["egress-watch"].Target != "www.gstatic.com:443" {
		t.Fatalf("operator task lost: %+v", byName["egress-watch"])
	}

	// disable the low path: the next sync drops its task, the rest stay
	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/paths/%d", lowID), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable low path: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/probe-tasks/sync-from-paths", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second sync: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, int(entryID)).Error; err != nil {
		t.Fatalf("reload entry: %v", err)
	}
	saved = agentproto.ProbeTask{}
	if err := json.Unmarshal([]byte(n.ProbeTasks), &saved); err != nil {
		t.Fatalf("restored probe_tasks: %v", err)
	}
	if len(saved.Specs) != 2 {
		t.Fatalf("after withdraw %d tasks (%+v); want 2", len(saved.Specs), saved.Specs)
	}
	for _, s := range saved.Specs {
		if s.Name == fmt.Sprintf("path-%d", lowID) {
			t.Fatalf("disabled path task must be dropped: %+v", saved.Specs)
		}
	}
}

// TestImportanceProbeInterval pins the F-0924 grade-to-interval mapping.
func TestImportanceProbeInterval(t *testing.T) {
	cases := map[string]int{"critical": 60, "normal": 300, "low": 900, "": 300, "bogus": 300}
	for imp, want := range cases {
		if got := importanceProbeInterval(imp); got != want {
			t.Fatalf("importanceProbeInterval(%q) = %d; want %d", imp, got, want)
		}
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
