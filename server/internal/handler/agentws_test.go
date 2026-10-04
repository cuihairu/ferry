package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// readEnv 从连接读一条消息并解析为 Envelope。
func readEnv(t *testing.T, c *websocket.Conn) agentproto.Envelope {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

// waitStatus 轮询直到节点状态符合期望。
func waitStatus(t *testing.T, r http.Handler, id int, want string) {
	t.Helper()
	path := "/api/nodes/" + strconv.Itoa(id)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		r.ServeHTTP(rec, req)
		var n map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		if n["status"] == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("node status never became %q", want)
}

func TestAgentWSHeartbeat(t *testing.T) {
	r := newTestRouter(t)

	// 造一个节点拿接入令牌
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-1", "address": "hk.example.com", "port": 443, "protocol": "vless",
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

	// hello → hello_ack
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "hk-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 心跳 → heartbeat_ack，节点置 online
	hb, _ := agentproto.NewEnvelope("hb-1", agentproto.MsgHeartbeat, agentproto.Heartbeat{
		UptimeSec: 10, Load1: 0.1, At: time.Now(),
	})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}
	waitStatus(t, r, 1, "online")

	// 断开后节点应回 offline
	c.Close()
	waitStatus(t, r, 1, "offline")
}

func TestAgentWSBadToken(t *testing.T) {
	r := newTestRouter(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{Token: "wrong"})
	_ = c.WriteJSON(hello)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = c.ReadMessage()
	if err == nil {
		t.Fatal("server must close on invalid token")
	}
	if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) && !websocket.IsUnexpectedCloseError(err) {
		t.Fatalf("unexpected close error: %v", err)
	}
}
