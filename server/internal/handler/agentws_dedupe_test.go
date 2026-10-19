package handler

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// newAgentConn 拨号 ws 并走 hello 认证，返回可用连接。
func newAgentConn(t *testing.T, srvURL, token, agentID string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srvURL, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello, _ := agentproto.NewEnvelope("hello", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: agentID, Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}
	return c
}

// trafficFrame 固定内容的 traffic 帧（同 ID 同字节，钉死「只看 ID 不看内容」）。
func trafficFrame(id string) agentproto.Envelope {
	env, _ := agentproto.NewEnvelope(id, agentproto.MsgTraffic, agentproto.TrafficReport{
		Items: []agentproto.ProcTraffic{{
			Proc: "xray", Rx: 1000, Tx: 500, At: time.Unix(1700000000, 0),
		}},
	})
	return env
}

func countTrafficRows(t *testing.T, db *gorm.DB, nodeID int64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&storage.NodeTrafficLog{}).Where("node_id = ?", nodeID).Count(&n).Error; err != nil {
		t.Fatalf("count node_traffic_logs: %v", err)
	}
	return n
}

// TestAgentWSDuplicateTrafficFrame 覆盖幂等判据 C1/C6：同连接同 ID 的 traffic
// 帧发两次，两次都收到字节等价的 ack，但流量只记一次（FERRY-004 §8）。
func TestAgentWSDuplicateTrafficFrame(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "dup-1", "address": "dup.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != 201 {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	nodeID := int64(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := newAgentConn(t, srv.URL, token, "dup-1")

	if err := c.WriteJSON(trafficFrame("t-dup-1")); err != nil {
		t.Fatalf("write frame 1: %v", err)
	}
	ack1 := readEnv(t, c)
	if err := c.WriteJSON(trafficFrame("t-dup-1")); err != nil {
		t.Fatalf("write frame 2 (same id): %v", err)
	}
	ack2 := readEnv(t, c)

	if ack1.Type != agentproto.MsgTrafficAck || ack2.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack twice, got %s / %s", ack1.Type, ack2.Type)
	}
	// C6：重复帧 ack 与首帧字节等价（agent 据此安心丢弃重传）。
	a1, _ := json.Marshal(ack1)
	a2, _ := json.Marshal(ack2)
	if !bytes.Equal(a1, a2) {
		t.Fatalf("ack not byte-equal on duplicate: %s vs %s", a1, a2)
	}
	var ta agentproto.TrafficAck
	if err := json.Unmarshal(ack1.Payload, &ta); err != nil || ta.Recorded != 1 {
		t.Fatalf("ack payload = %+v err=%v", ack1.Payload, err)
	}
	// C1：副作用只执行一次。
	if got := countTrafficRows(t, db, nodeID); got != 1 {
		t.Fatalf("node_traffic_logs rows = %d; want 1", got)
	}
}

// TestAgentWSDedupSurvivesReconnect 覆盖判据 C2：断开重连后同 ID 帧仍去重
// （幂等窗口按 node_id 存活于面板进程，不随连接销毁）。
func TestAgentWSDedupSurvivesReconnect(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "rc-1", "address": "rc.example.com", "port": 443, "protocol": "vless",
	})
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	nodeID := int64(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()

	c1 := newAgentConn(t, srv.URL, token, "rc-1")
	if err := c1.WriteJSON(trafficFrame("t-rc-1")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if env := readEnv(t, c1); env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack, got %s", env.Type)
	}
	c1.Close()

	c2 := newAgentConn(t, srv.URL, token, "rc-1")
	defer c2.Close()
	if err := c2.WriteJSON(trafficFrame("t-rc-1")); err != nil {
		t.Fatalf("write frame after reconnect: %v", err)
	}
	if env := readEnv(t, c2); env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack after reconnect, got %s", env.Type)
	}
	if got := countTrafficRows(t, db, nodeID); got != 1 {
		t.Fatalf("rows after reconnect replay = %d; want 1", got)
	}
}

// TestAgentWSNewIDIsNewFrame 覆盖判据 C3：同内容换新 ID 正常记账
// （判据只看 ID 不看内容）。
func TestAgentWSNewIDIsNewFrame(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "new-1", "address": "new.example.com", "port": 443, "protocol": "vless",
	})
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	nodeID := int64(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := newAgentConn(t, srv.URL, token, "new-1")
	defer c.Close()

	if err := c.WriteJSON(trafficFrame("t-new-1")); err != nil {
		t.Fatalf("write frame 1: %v", err)
	}
	readEnv(t, c)
	if err := c.WriteJSON(trafficFrame("t-new-2")); err != nil {
		t.Fatalf("write frame 2: %v", err)
	}
	readEnv(t, c)
	if got := countTrafficRows(t, db, nodeID); got != 2 {
		t.Fatalf("rows = %d; want 2 (new id = new frame)", got)
	}
}

// TestAgentWSEmptyIDNeverDeduped 覆盖判据 C4：空 ID 每帧都处理（向前兼容
// 旧 agent 的 proc_report 空 ID 口径）。
func TestAgentWSEmptyIDNeverDeduped(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "empty-1", "address": "empty.example.com", "port": 443, "protocol": "vless",
	})
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	nodeID := int64(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := newAgentConn(t, srv.URL, token, "empty-1")
	defer c.Close()

	for i := 0; i < 2; i++ {
		if err := c.WriteJSON(trafficFrame("")); err != nil {
			t.Fatalf("write frame %d: %v", i+1, err)
		}
		if env := readEnv(t, c); env.Type != agentproto.MsgTrafficAck {
			t.Fatalf("frame %d: expected traffic_ack, got %s", i+1, env.Type)
		}
	}
	if got := countTrafficRows(t, db, nodeID); got != 2 {
		t.Fatalf("rows = %d; want 2 (empty id never deduped)", got)
	}
}

// TestAgentWSSameIDAcrossTypes 覆盖判据 C5：判据键含消息类型，
// traffic 与 alarm 撞 ID 各自处理一次。
func TestAgentWSSameIDAcrossTypes(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "cross-1", "address": "cross.example.com", "port": 443, "protocol": "vless",
	})
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	nodeID := int64(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := newAgentConn(t, srv.URL, token, "cross-1")
	defer c.Close()

	alarm, _ := agentproto.NewEnvelope("x-1", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindHighLoad, Severity: agentproto.AlarmSeverityWarning, Message: "hot",
	})
	if err := c.WriteJSON(alarm); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", env.Type)
	}
	if err := c.WriteJSON(trafficFrame("x-1")); err != nil {
		t.Fatalf("write traffic: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack, got %s", env.Type)
	}

	var n int64
	if err := db.Model(&storage.NodeTrafficLog{}).Where("node_id = ?", nodeID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("traffic rows = %d err=%v; want 1", n, err)
	}
	if err := db.Model(&storage.Alert{}).Where("node_id = ?", nodeID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("alert rows = %d err=%v; want 1 (both types processed)", n, err)
	}
}

// TestReportDeduperWindow 覆盖判据 C7（容量界）：FIFO 挤出后的同 ID 视为新帧；
// 判据键含类型；空 ID 恒不去重。
func TestReportDeduperWindow(t *testing.T) {
	d := newReportDeduperCap(2, time.Minute)
	if d.repeat(agentproto.MsgTraffic, "a") {
		t.Fatal("first a must not be a repeat")
	}
	if !d.repeat(agentproto.MsgTraffic, "a") {
		t.Fatal("second a must be a repeat")
	}
	if d.repeat(agentproto.MsgTraffic, "b") {
		t.Fatal("first b must not be a repeat")
	}
	d.repeat(agentproto.MsgTraffic, "c") // 窗口满，挤出最早的 a
	if d.repeat(agentproto.MsgTraffic, "a") {
		t.Fatal("evicted a must be treated as a new frame")
	}
	if d.repeat(agentproto.MsgAlarm, "b") {
		t.Fatal("same id different type must not be a repeat")
	}
	if d.repeat(agentproto.MsgTraffic, "") {
		t.Fatal("empty id must never be deduped")
	}
}

// TestReportDeduperTTL 覆盖判据 C7（时间界）：TTL 过期后的同 ID 视为新帧。
func TestReportDeduperTTL(t *testing.T) {
	d := newReportDeduperCap(4, 30*time.Millisecond)
	if d.repeat(agentproto.MsgTraffic, "a") {
		t.Fatal("first a must not be a repeat")
	}
	time.Sleep(60 * time.Millisecond)
	if d.repeat(agentproto.MsgTraffic, "a") {
		t.Fatal("expired a must be treated as a new frame")
	}
}
