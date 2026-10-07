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

// TestAgentWSProcLogs 覆盖节点进程日志拉取（P1-11）：面板请求经 WS 到
// agent、应答回传；离线 502、agent 报未知进程 400、参数本地校验 400。
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

	// 离线：未连接 502
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray", id), nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("offline should 502: %d %s", rec.Code, rec.Body)
	}
	// 参数本地校验：缺 proc / 非法 limit
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs", id), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing proc should 400: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray&limit=0", id), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit should 400: %d", rec.Code)
	}

	// 连上假 agent
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

	// 正常拉取：请求阻塞等 ack，假 agent 回最近日志
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

	// agent 报未知进程 → 400
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
