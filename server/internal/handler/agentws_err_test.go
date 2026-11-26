package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// Batch 26 coverage fill: agentws error branches -- the handshake gate (non-WS upgrade /
// first message not hello / bad hello / bad frame / bad protocol version), a token-check DB
// failure, node row update failures (version and metadata write-backs only log), payload
// decode failures on the five report types letting the loop continue, three persistence
// failures that only log while still answering, unknown message types being noted, plus
// direct tests of the persistence helpers' empty inputs and the address-resolution fallback.
// Left alone: the six send-side ack failure branches (hc.Send on hello/heartbeat/alarm/probe/
// traffic/calibrate) need the peer to drop the link before the server writes, which is a race
// on timing and is not forcibly provoked.

// wsNode creates a node and returns its access token and row id.
func wsNode(t *testing.T, r *gin.Engine, name string) (string, int) {
	t.Helper()
	rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	return node["token"].(string), int(node["id"].(float64))
}

// wsDial starts an httptest server and dials the agent WebSocket.
func wsDial(t *testing.T, r *gin.Engine) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(r)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/agent/ws", nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	return c, srv
}

// wsHandshake completes the auth handshake and returns the connection and the hello_ack envelope.
func wsHandshake(t *testing.T, r *gin.Engine, token string) (*websocket.Conn, *httptest.Server, agentproto.Envelope) {
	t.Helper()
	c, srv := wsDial(t, r)
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello,
		agentproto.Hello{Token: token, AgentID: "a-1", Version: "test"})
	if err := c.WriteJSON(hello); err != nil {
		srv.Close()
		c.Close()
		t.Fatalf("write hello: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgHelloAck {
		srv.Close()
		c.Close()
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}
	return c, srv, env
}

// wsReadClose reads until the server's close frame and asserts the close code.
func wsReadClose(t *testing.T, c *websocket.Conn, want int) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := c.ReadMessage(); !websocket.IsCloseError(err, want) {
		t.Fatalf("want close %d, got %v", want, err)
	}
}

// isNodeModel reports whether the statement lands on the node table (in a Find chain Model is a slice pointer).
func isNodeModel(v any) bool {
	switch v.(type) {
	case *storage.Node, []storage.Node, *[]storage.Node:
		return true
	}
	return false
}

// failNodeUpdates makes node row updates fail (the "version/metadata/status write-back only logs" policy).
func failNodeUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:node_update", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// isProbeReportDest / isNodeTrafficDest / isNodeProcStatusDest report whether the statement
// lands on the corresponding table: a batch Create may leave Model unset, so both Model and Dest
// are checked.
func isProbeReportDest(v any) bool {
	switch v.(type) {
	case *storage.ProbeReport, []storage.ProbeReport, *[]storage.ProbeReport:
		return true
	}
	return false
}

func isNodeTrafficDest(v any) bool {
	switch v.(type) {
	case *storage.NodeTrafficLog, []storage.NodeTrafficLog, *[]storage.NodeTrafficLog:
		return true
	}
	return false
}

func isNodeProcStatusDest(v any) bool {
	switch v.(type) {
	case *storage.NodeProcStatus, []storage.NodeProcStatus, *[]storage.NodeProcStatus:
		return true
	}
	return false
}

// failCreatesOn makes row creation fail for statements matching the predicate.
func failCreatesOn(t *testing.T, db *gorm.DB, match func(any) bool, name string) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// TestAgentWSUpgradeReject: a non-WebSocket request whose upgrade fails returns 400.
func TestAgentWSUpgradeReject(t *testing.T) {
	r := newTestRouter(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/agent/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain get: %d", resp.StatusCode)
	}
}

// TestAgentWSHelloGate covers the handshake gate: a first message that is not hello, a hello
// missing its token, a bad frame, and a bad protocol version all close with a protocol error.
func TestAgentWSHelloGate(t *testing.T) {
	r := newTestRouter(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	// The first message is not hello.
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hb, _ := agentproto.NewEnvelope("hb", agentproto.MsgHeartbeat, agentproto.Heartbeat{})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	wsReadClose(t, c, websocket.CloseProtocolError)
	c.Close()

	// hello payload missing the token.
	c, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello, _ := agentproto.NewEnvelope("h", agentproto.MsgHello, agentproto.Hello{})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	wsReadClose(t, c, websocket.CloseProtocolError)
	c.Close()

	// Bad frame (non-JSON text).
	c, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	wsReadClose(t, c, websocket.CloseProtocolError)
	c.Close()

	// Protocol version mismatch (rejected by readEnvelope's Valid).
	c, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.WriteMessage(websocket.TextMessage, []byte(`{"v":0,"id":"x","type":"hello"}`)); err != nil {
		t.Fatalf("write version: %v", err)
	}
	wsReadClose(t, c, websocket.CloseProtocolError)
	c.Close()
}

// TestAgentWSAuthDBError covers a DB failure in token validation: errors other than not-found also close as auth failure.
func TestAgentWSAuthDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	wsNode(t, r, "auth-db")
	closeSQL(t, db)
	srv := httptest.NewServer(r)
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{Token: "t-1"})
	_ = c.WriteJSON(hello)
	wsReadClose(t, c, websocket.ClosePolicyViolation)
}

// TestAgentWSNodeWriteFailures covers node row update failures: metadata backfill and version
// persistence only log, while the handshake and hello_ack proceed normally (the panel's
// authoritative values fall back to empty).
func TestAgentWSNodeWriteFailures(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token, _ := wsNode(t, r, "wfail-1")
	failNodeUpdates(t, db)
	c, srv, env := wsHandshake(t, r, token)
	defer srv.Close()
	defer c.Close()

	var ack agentproto.HelloAck
	if err := env.Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.Meta.Role != "" || ack.Meta.Region != "" {
		t.Fatalf("meta should fall back empty: %+v", ack.Meta)
	}
}

// TestAgentWSDecodeContinue covers payload decode failures on the six message types: each is
// skipped and the read loop continues, and a later valid heartbeat is still answered.
func TestAgentWSDecodeContinue(t *testing.T) {
	r := newTestRouter(t)
	token, _ := wsNode(t, r, "dec-1")
	c, srv, _ := wsHandshake(t, r, token)
	defer srv.Close()
	defer c.Close()

	for _, typ := range []string{
		agentproto.MsgHeartbeat, agentproto.MsgAlarm, agentproto.MsgProbeReport,
		agentproto.MsgProcReport, agentproto.MsgTraffic, agentproto.MsgCalibrate,
	} {
		env, _ := agentproto.NewEnvelope(typ+"-junk", typ, "junk")
		if err := c.WriteJSON(env); err != nil {
			t.Fatalf("write %s: %v", typ, err)
		}
	}
	// The loop is still alive: a valid heartbeat answer is reachable.
	hb, _ := agentproto.NewEnvelope("hb-ok", agentproto.MsgHeartbeat,
		agentproto.Heartbeat{UptimeSec: 1, At: time.Now()})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}
}

// TestAgentWSSaveFailureLogs covers three persistence failures: process snapshot creation, probe
// verdict batch persistence, and node traffic / per-user traffic query failures all only log,
// and each ack is still sent.
func TestAgentWSSaveFailureLogs(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token, _ := wsNode(t, r, "savef-1")
	failCreatesOn(t, db, isNodeProcStatusDest, "test:procstatus_create")
	failCreatesOn(t, db, isProbeReportDest, "test:probereport_create")
	failCreatesOn(t, db, isNodeTrafficDest, "test:nodetraffic_create")
	if err := db.Migrator().DropTable(&storage.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	c, srv, _ := wsHandshake(t, r, token)
	defer srv.Close()
	defer c.Close()

	// Heartbeat carrying a process snapshot: creation fails -> log, ack still sent.
	hb, _ := agentproto.NewEnvelope("hb-1", agentproto.MsgHeartbeat, agentproto.Heartbeat{
		UptimeSec: 1, At: time.Now(),
		Procs: []agentproto.ProcStatus{{Name: "xray", State: "running"}},
	})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}

	// Probe verdicts: batch persistence fails -> log, ack count still returned.
	batch, _ := agentproto.NewEnvelope("pr-1", agentproto.MsgProbeReport, agentproto.ProbeReportBatch{
		Items: []agentproto.ProbeReport{{
			TargetKind: agentproto.ProbeTargetTunnel, TargetNode: 1,
			Direction: agentproto.DirectionOut, RttMs: 48, Reachable: true,
			Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now(),
		}},
	})
	if err := c.WriteJSON(batch); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgProbeAck {
		t.Fatalf("expected probe_ack, got %s", env.Type)
	}

	// Traffic report: node traffic persistence fails + users-table-missing query fails -> two logs, ack still sent.
	rep, _ := agentproto.NewEnvelope("tr-1", agentproto.MsgTraffic, agentproto.TrafficReport{
		Items: []agentproto.ProcTraffic{{
			Proc: "xray", Rx: 1, Tx: 1, At: time.Now(),
			Users: []agentproto.UserTraffic{{Email: "alice", Rx: 1, Tx: 1}},
		}},
	})
	if err := c.WriteJSON(rep); err != nil {
		t.Fatalf("write traffic: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack, got %s", env.Type)
	}
}

// TestAgentWSUnhandledType covers an unknown message type: with no request waiting it is only noted, and the loop continues.
func TestAgentWSUnhandledType(t *testing.T) {
	r := newTestRouter(t)
	token, _ := wsNode(t, r, "unhd-1")
	c, srv, _ := wsHandshake(t, r, token)
	defer srv.Close()
	defer c.Close()

	env, _ := agentproto.NewEnvelope("u1", "bogus.type", nil)
	if err := c.WriteJSON(env); err != nil {
		t.Fatalf("write unknown: %v", err)
	}
	hb, _ := agentproto.NewEnvelope("hb-ok", agentproto.MsgHeartbeat,
		agentproto.Heartbeat{UptimeSec: 1, At: time.Now()})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if got := readEnv(t, c); got.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", got.Type)
	}
}

// TestSaveReportUnits is a direct test of the persistence helpers: empty inputs short-circuit,
// target node snapshot completion, all-unregistered emails short-circuit, a missing users table
// surfacing an error, and the three address-resolution fallbacks.
func TestSaveReportUnits(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	_, id := wsNode(t, r, "unit-1")
	// The metering-scope guard (F-1242/F-1243) drops landing reports before the
	// user query runs; this test exercises the query error itself, so the node
	// must be a metering node.
	if err := db.Model(&storage.Node{}).Where("id = ?", id).Update("role", "both").Error; err != nil {
		t.Fatalf("set node role: %v", err)
	}

	// Empty inputs short-circuit.
	if err := h.saveProbeReports(int64(id), nil); err != nil {
		t.Fatalf("empty probe reports: %v", err)
	}
	if err := h.saveNodeTraffic(int64(id), nil); err != nil {
		t.Fatalf("empty node traffic: %v", err)
	}

	// Target node hits by ID: the region/ISP snapshot is completed.
	if err := db.Model(&storage.Node{}).Where("id=?", id).
		Updates(map[string]any{"region": "华东", "isp": "电信"}).Error; err != nil {
		t.Fatalf("set region: %v", err)
	}
	if err := h.saveProbeReports(int64(id), []agentproto.ProbeReport{{
		TargetKind: agentproto.ProbeTargetTunnel, TargetNode: 1,
		Direction: agentproto.DirectionOut, RttMs: 20, Reachable: true,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now(),
	}}); err != nil {
		t.Fatalf("save probe by node: %v", err)
	}
	var row storage.ProbeReport
	if err := db.First(&row).Error; err != nil {
		t.Fatalf("load probe row: %v", err)
	}
	if row.TargetNodeID == nil || *row.TargetNodeID != uint(id) || row.Region != "华东" || row.ISP != "电信" {
		t.Fatalf("snapshot mismatch: %+v", row)
	}

	// Per-user traffic: all emails unregistered -> no rows to write, short-circuit success.
	if err := h.saveUserTraffic(int64(id), []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "ghost@example.com", Rx: 1, Tx: 1}},
	}}); err != nil {
		t.Fatalf("all unknown emails: %v", err)
	}

	// Users table missing: the query failure surfaces as an error.
	if err := db.Migrator().DropTable(&storage.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	if err := h.saveUserTraffic(int64(id), []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "alice", Rx: 1, Tx: 1}},
	}}); err == nil {
		t.Fatal("want user query error")
	}

	// Address resolution fallbacks: no port, non-numeric port, node not found.
	if h.findNodeByAddress("no-port-here") != nil {
		t.Fatal("want nil for missing port")
	}
	if h.findNodeByAddress("host:notaport") != nil {
		t.Fatal("want nil for bad port")
	}
	if h.findNodeByAddress("missing.example.com:443") != nil {
		t.Fatal("want nil for unknown node")
	}
}

// TestSyncNodeMetaFills covers metadata completion: under panel ownership, blank fields are
// filled from the report; a failed fill falls back to the row value without leaking the reported
// value; and a failed node row load falls back to empty authoritative values.
func TestSyncNodeMetaFills(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	_, id := wsNode(t, r, "meta-1")
	// The node row carries GORM defaults such as default:unknown (backfilled at row creation), so
	// first clear the fillable fields to blank (an explicit empty write via a map update) and hand
	// over metadata ownership.
	if err := db.Model(&storage.Node{}).Where("id=?", id).Updates(map[string]any{
		"city": "", "datacenter": "", "isp": "", "meta_init": true,
	}).Error; err != nil {
		t.Fatalf("blank meta: %v", err)
	}

	// The blank ISP fill succeeds: both the return value and the row value carry the filled result.
	meta := h.syncNodeMeta(int64(id), agentproto.NodeMeta{ISP: "电信"})
	if meta.ISP != "电信" {
		t.Fatalf("fill isp: %+v", meta)
	}
	var n storage.Node
	if err := db.First(&n, id).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if n.ISP != "电信" {
		t.Fatalf("row isp: %q", n.ISP)
	}

	// A failed fill: fall back to the row value (city still empty), without leaking the reported value.
	failNodeUpdates(t, db)
	meta = h.syncNodeMeta(int64(id), agentproto.NodeMeta{City: "上海"})
	if meta.City != "" {
		t.Fatalf("fallback should keep row value, got %+v", meta)
	}

	// The node row fails to load: fall back to empty authoritative values.
	closeSQL(t, db)
	if meta := h.syncNodeMeta(int64(id), agentproto.NodeMeta{City: "南京"}); meta.City != "" || meta.ISP != "" {
		t.Fatalf("closed db should fall back empty: %+v", meta)
	}
}
