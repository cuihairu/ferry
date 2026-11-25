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
	"github.com/gorilla/websocket"
)

// TestAgentWSProcLogs covers node process log fetching (P1-11): the panel request
// travels over WS to the agent and the reply is relayed back; offline -> 502,
// agent reporting an unknown proc -> 400, local param validation -> 400.
func TestAgentWSProcLogs(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "logs-1", "address": "tw.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	id := int(node["id"].(float64))

	// Offline: not connected -> 502
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray", id), nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("offline should 502: %d %s", rec.Code, rec.Body)
	}
	// Local param validation: missing proc / invalid limit
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs", id), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing proc should 400: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray&limit=0", id), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit should 400: %d", rec.Code)
	}

	// Connect the fake agent
	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "logs-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// Normal fetch: the request blocks waiting for the ack, and the fake agent replies with recent logs
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray&limit=50", id), nil)
	}()

	env := readEnv(t, c)
	if env.Type != agentproto.MsgProcLogs {
		t.Fatalf("expected proc_logs, got %s", env.Type)
	}
	var req agentproto.ProcLogsReq
	if err := env.Decode(&req); err != nil {
		t.Fatalf("decode proc_logs: %v", err)
	}
	if req.Proc != "xray" || req.Limit != 50 {
		t.Fatalf("proc_logs request mismatch: %+v", req)
	}
	ack, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProcLogsAck,
		agentproto.ProcLogsAck{Proc: req.Proc, Lines: []string{"l1", "l2"}})
	if err := c.WriteJSON(ack); err != nil {
		t.Fatalf("write ack: %v", err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("logs: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Proc  string   `json:"proc"`
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Proc != "xray" || len(out.Lines) != 2 || out.Lines[0] != "l1" {
			t.Fatalf("logs response mismatch: %+v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logs request timed out")
	}

	// Agent reports an unknown proc -> 400
	go func() {
		done <- doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=nope", id), nil)
	}()
	env = readEnv(t, c)
	if env.Type != agentproto.MsgProcLogs {
		t.Fatalf("expected proc_logs, got %s", env.Type)
	}
	ack, _ = agentproto.NewEnvelope(env.ID, agentproto.MsgProcLogsAck,
		agentproto.ProcLogsAck{Proc: "nope", Error: `unknown proc "nope"`})
	if err := c.WriteJSON(ack); err != nil {
		t.Fatalf("write ack: %v", err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown proc should 400: %d %s", rec.Code, rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unknown proc request timed out")
	}
}
