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

// newAgentConn dials the ws and goes through hello auth, returning a usable connection.
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

// trafficFrame is a traffic frame with fixed content (same ID, same bytes), pinning down the
// "ID decides, content does not" criterion.
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

// TestAgentWSDuplicateTrafficFrame covers idempotency criteria C1/C6: two traffic frames with
// the same ID on the same connection are sent twice, both receive byte-equal acks, but the traffic
// is only recorded once (FERRY-004 §8).
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
	// C6: the duplicate frame's ack is byte-equal to the first frame's (so the agent can safely drop retransmissions).
	a1, _ := json.Marshal(ack1)
	a2, _ := json.Marshal(ack2)
	if !bytes.Equal(a1, a2) {
		t.Fatalf("ack not byte-equal on duplicate: %s vs %s", a1, a2)
	}
	var ta agentproto.TrafficAck
	if err := json.Unmarshal(ack1.Payload, &ta); err != nil || ta.Recorded != 1 {
		t.Fatalf("ack payload = %+v err=%v", ack1.Payload, err)
	}
	// C1: the side effect executes exactly once.
	if got := countTrafficRows(t, db, nodeID); got != 1 {
		t.Fatalf("node_traffic_logs rows = %d; want 1", got)
	}
}

// TestAgentWSDedupSurvivesReconnect covers criterion C2: an identical frame is still deduplicated
// after a disconnect/reconnect (the idempotency window lives on node_id inside the panel process and
// is not destroyed with the connection).
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

// TestAgentWSNewIDIsNewFrame covers criterion C3: same content with a new ID records normally
// (the criterion looks at the ID only, not the content).
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

// TestAgentWSEmptyIDNeverDeduped covers criterion C4: an empty ID is processed for every frame
// (forward compatibility with old agents' proc_report convention of an empty ID).
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

// TestAgentWSSameIDAcrossTypes covers criterion C5: the criterion key includes the message type,
// so traffic and alarm colliding on the same ID are each processed once.
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

// TestReportDeduperWindow covers criterion C7 (capacity bound): after a FIFO eviction the same ID is
// treated as a new frame; the criterion key includes the type; an empty ID is never deduplicated.
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
	d.repeat(agentproto.MsgTraffic, "c") // window is full, evicting the oldest a
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

// TestReportDeduperTTL covers criterion C7 (time bound): after the TTL expires, the same ID is
// treated as a new frame.
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
