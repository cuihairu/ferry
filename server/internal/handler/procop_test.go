package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// fakeAgent opens an agent WS connection, completes the hello handshake, and returns the connection.
func fakeAgent(t *testing.T, srvURL, name, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srvURL, "http") + "/agent/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: name, Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}
	return c
}

// replyAck sends a same-ID ack reply to a pending request.
func replyAck(t *testing.T, c *websocket.Conn, req agentproto.Envelope, msgType string, payload any) {
	t.Helper()
	env, err := agentproto.NewEnvelope(req.ID, msgType, payload)
	if err != nil {
		t.Fatalf("build ack: %v", err)
	}
	if err := c.WriteJSON(env); err != nil {
		t.Fatalf("write ack: %v", err)
	}
}

func createNodeFor(t *testing.T, r *gin.Engine, name string) (uint, string) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	return uint(node["id"].(float64)), node["token"].(string)
}

// TestNodeProcOp covers single-node process operation (A-15 base path): success,
// agent error, offline, invalid action.
func TestNodeProcOp(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "ctl-1")

	// Offline -> 502
	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id), map[string]any{"action": "reload"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline should 502: %d %s", rec.Code, rec.Body)
	}
	// Invalid action -> 400
	rec = doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id), map[string]any{"action": "dance"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action should 400: %d", rec.Code)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "ctl-1", token)

	// Success: agent replies ok
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id), map[string]any{"action": "reload", "proc": "xray"})
	}()
	env := readEnv(t, c)
	if env.Type != agentproto.MsgProcCtl {
		t.Fatalf("expected proc_ctl, got %s", env.Type)
	}
	var ctl agentproto.ProcCtl
	if err := env.Decode(&ctl); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ctl.Proc != "xray" || ctl.Action != "reload" {
		t.Fatalf("proc_ctl mismatch: %+v", ctl)
	}
	replyAck(t, c, env, agentproto.MsgProcCtlAck, agentproto.ProcCtlAck{Proc: ctl.Proc, Action: ctl.Action, OK: true})
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("proc op: %d %s", rec.Code, rec.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["ok"] != true || out["action"] != "reload" {
			t.Fatalf("proc op response mismatch: %v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proc op timed out")
	}

	// Agent error (unknown proc) -> 400
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id), map[string]any{"action": "start", "proc": "nope"})
	}()
	env = readEnv(t, c)
	replyAck(t, c, env, agentproto.MsgProcCtlAck, agentproto.ProcCtlAck{Proc: "nope", Action: "start", Error: `unknown proc "nope"`})
	select {
	case rec := <-done:
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("agent error should 400: %d %s", rec.Code, rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent error case timed out")
	}
}

// TestBatchNodeProc covers batch process operation (A-15): two online nodes
// succeed concurrently + per-node replies for the offline node + param validation.
func TestBatchNodeProc(t *testing.T) {
	r := newTestRouter(t)
	idA, tokenA := createNodeFor(t, r, "bat-a")
	idB, tokenB := createNodeFor(t, r, "bat-b")
	idOffline, _ := createNodeFor(t, r, "bat-off")

	// Param validation
	rec := doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{"ids": []uint{}, "action": "stop"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty ids should 400: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{"ids": []uint{idA}, "action": "dance"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action should 400: %d", rec.Code)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	ca := fakeAgent(t, srv.URL, "bat-a", tokenA)
	cb := fakeAgent(t, srv.URL, "bat-b", tokenB)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{
			"ids": []uint{idA, idOffline, idB}, "action": "stop",
		})
	}()

	// Both online agents each receive a proc_ctl and reply with an ack
	for _, conn := range []*websocket.Conn{ca, cb} {
		env := readEnv(t, conn)
		if env.Type != agentproto.MsgProcCtl {
			t.Fatalf("expected proc_ctl, got %s", env.Type)
		}
		var ctl agentproto.ProcCtl
		if err := env.Decode(&ctl); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ctl.Action != "stop" || ctl.Proc != "xray" {
			t.Fatalf("batch proc_ctl mismatch: %+v", ctl)
		}
		replyAck(t, conn, env, agentproto.MsgProcCtlAck, agentproto.ProcCtlAck{Proc: ctl.Proc, Action: ctl.Action, OK: true})
	}

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("batch proc: %d %s", rec.Code, rec.Body)
		}
		var out []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("results = %d, want 3", len(out))
		}
		byID := map[uint]map[string]any{}
		for _, row := range out {
			byID[uint(row["node_id"].(float64))] = row
		}
		if byID[idA]["ok"] != true || byID[idB]["ok"] != true {
			t.Fatalf("online nodes should ok: %v %v", byID[idA], byID[idB])
		}
		if byID[idOffline]["ok"] != false {
			t.Fatalf("offline node should fail: %v", byID[idOffline])
		}
		if s, _ := byID[idOffline]["error"].(string); s == "" {
			t.Fatalf("offline node should carry error: %v", byID[idOffline])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch proc timed out")
	}
}

// TestBatchNodeConfig covers batch config dispatch (A-15): per-node record + replies.
func TestBatchNodeConfig(t *testing.T) {
	r := newTestRouter(t)
	idA, tokenA := createNodeFor(t, r, "bcfg-a")
	idOffline, _ := createNodeFor(t, r, "bcfg-off")

	srv := httptest.NewServer(r)
	defer srv.Close()
	ca := fakeAgent(t, srv.URL, "bcfg-a", tokenA)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", "/api/nodes/batch/config", map[string]any{
			"ids": []uint{idA, idOffline}, "proc": "xray", "kind": "xray", "payload": `{"inbounds":[]}`,
		})
	}()

	env := readEnv(t, ca)
	if env.Type != agentproto.MsgConfigPush {
		t.Fatalf("expected config_push, got %s", env.Type)
	}
	var cp agentproto.ConfigPush
	if err := env.Decode(&cp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	replyAck(t, ca, env, agentproto.MsgConfigAck, agentproto.ConfigAck{Proc: cp.Proc, Version: cp.Version, OK: true, Validated: true})

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("batch config: %d %s", rec.Code, rec.Body)
		}
		var out []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(out) != 2 {
			t.Fatalf("results = %d, want 2", len(out))
		}
		byID := map[uint]map[string]any{}
		for _, row := range out {
			byID[uint(row["node_id"].(float64))] = row
		}
		if byID[idA]["ok"] != true || byID[idA]["status"] != "applied" {
			t.Fatalf("online node mismatch: %v", byID[idA])
		}
		if byID[idOffline]["ok"] != false {
			t.Fatalf("offline node should fail: %v", byID[idOffline])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch config timed out")
	}

	// Record: the online node's node_config is persisted in the DB
	hrec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/configs", idA), nil)
	if hrec.Code != http.StatusOK {
		t.Fatalf("list configs: %d", hrec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(hrec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode configs: %v", err)
	}
	if len(rows) != 1 || rows[0]["status"] != "applied" {
		t.Fatalf("node_config rows mismatch: %v", rows)
	}
}
