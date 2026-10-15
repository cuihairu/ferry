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

// 二十六批补口：agentws 错误分支——握手关卡（非 WS 升级/首条非 hello/坏
// hello/坏帧/坏协议版本）、令牌校验库故障、节点行更新失败（版本与元数据
// 回写只记日志）、五类上报载荷解码失败后循环继续、三类落库失败只记日志且
// 照常应答、未知消息类型留痕，以及落存函数空入参与地址解析回落的直测。
// 留白：六处发送侧 ack 失败分支（hello/heartbeat/alarm/probe/traffic/
// calibrate 的 hc.Send）需对端在服务端写出前断链，属竞态时序，不硬凑。

// wsNode 造节点并返回接入令牌与行 id。
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

// wsDial 起 httptest 服务并拨 agent WebSocket。
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

// wsHandshake 完成认证握手，返回连接与 hello_ack 信封。
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

// wsReadClose 读到服务端关闭帧并断言关闭码。
func wsReadClose(t *testing.T, c *websocket.Conn, want int) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := c.ReadMessage(); !websocket.IsCloseError(err, want) {
		t.Fatalf("want close %d, got %v", want, err)
	}
}

// isNodeModel 判断语句是否落在节点表上（Find 链 Model 是切片指针）。
func isNodeModel(v any) bool {
	switch v.(type) {
	case *storage.Node, []storage.Node, *[]storage.Node:
		return true
	}
	return false
}

// failNodeUpdates 让节点行更新失败（版本/元数据/状态回写只记日志的口径）。
func failNodeUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:node_update", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// isProbeReportDest / isNodeTrafficDest / isNodeProcStatusDest 判断语句是否
// 落在对应表上：批量 Create 的 Model 可能未置位，Model 与 Dest 双查。
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

// failCreatesOn 让命中匹配的行创建失败。
func failCreatesOn(t *testing.T, db *gorm.DB, match func(any) bool, name string) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if match(tx.Statement.Model) || match(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// TestAgentWSUpgradeReject 非 WebSocket 请求升级失败即 400 返回。
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

// TestAgentWSHelloGate 握手关卡：首条非 hello、hello 缺令牌、坏帧与坏协议
// 版本一律协议错误关闭。
func TestAgentWSHelloGate(t *testing.T) {
	r := newTestRouter(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	// 首条消息不是 hello。
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

	// hello 载荷缺令牌。
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

	// 坏帧（非 JSON 文本）。
	c, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	wsReadClose(t, c, websocket.CloseProtocolError)
	c.Close()

	// 协议版本不符（readEnvelope 的 Valid 拒收）。
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

// TestAgentWSAuthDBError 令牌校验的库故障：非未找到类错误同样按鉴权失败关闭。
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

// TestAgentWSNodeWriteFailures 节点行更新失败：元数据回填与版本落库只记
// 日志，握手与 hello_ack 照常（面板权威回落为空值）。
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

// TestAgentWSDecodeContinue 六类消息载荷解码失败：逐条跳过继续读循环，
// 后续合法心跳照常应答。
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
	// 循环仍在：合法心跳应答可达。
	hb, _ := agentproto.NewEnvelope("hb-ok", agentproto.MsgHeartbeat,
		agentproto.Heartbeat{UptimeSec: 1, At: time.Now()})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}
}

// TestAgentWSSaveFailureLogs 三类落库失败：进程快照建档、探测结论批量落库、
// 节点流量与逐用户流量查询失败都只记日志，各 ack 照发。
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

	// 心跳带进程快照：建档失败 → 记日志，ack 照发。
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

	// 探测结论：批量落库失败 → 记日志，ack 条数照回。
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

	// 流量上报：节点流量落库失败 + 用户表缺失查询失败 → 两处记日志，ack 照发。
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

// TestAgentWSUnhandledType 未知消息类型：无等待请求即留痕，循环继续。
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

// TestSaveReportUnits 落存函数直测：空入参短路、目标节点快照补全、全部
// 未注册邮箱短路、用户表缺失冒错、地址解析三类回落。
func TestSaveReportUnits(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	_, id := wsNode(t, r, "unit-1")

	// 空入参短路。
	if err := h.saveProbeReports(int64(id), nil); err != nil {
		t.Fatalf("empty probe reports: %v", err)
	}
	if err := h.saveNodeTraffic(int64(id), nil); err != nil {
		t.Fatalf("empty node traffic: %v", err)
	}

	// 目标节点按 ID 命中：补全区域/运营商快照。
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

	// 逐用户流量：全部未注册邮箱 → 无行可落，短路成功。
	if err := h.saveUserTraffic(int64(id), []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "ghost@example.com", Rx: 1, Tx: 1}},
	}}); err != nil {
		t.Fatalf("all unknown emails: %v", err)
	}

	// 用户表缺失：查询失败冒错。
	if err := db.Migrator().DropTable(&storage.User{}); err != nil {
		t.Fatalf("drop users: %v", err)
	}
	if err := h.saveUserTraffic(int64(id), []agentproto.ProcTraffic{{
		Proc: "xray", At: time.Now(),
		Users: []agentproto.UserTraffic{{Email: "alice", Rx: 1, Tx: 1}},
	}}); err == nil {
		t.Fatal("want user query error")
	}

	// 地址解析回落：无端口、非数字端口、查无节点。
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

// TestSyncNodeMetaFills 元数据补全：面板接管下按上报补空白字段；补写失败
// 回落行值不外泄上报值；节点行加载失败回落空权威值。
func TestSyncNodeMetaFills(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	_, id := wsNode(t, r, "meta-1")
	// 节点行带 default:未知 等 GORM 缺省（建行即回填），先清出空白可补字段
	// （map 更新显式写空）并接管元数据。
	if err := db.Model(&storage.Node{}).Where("id=?", id).Updates(map[string]any{
		"city": "", "datacenter": "", "isp": "", "meta_init": true,
	}).Error; err != nil {
		t.Fatalf("blank meta: %v", err)
	}

	// ISP 空白补全成功：回值与行值都带上补全结果。
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

	// 补写失败：回落行值（城市仍空），上报值不外泄。
	failNodeUpdates(t, db)
	meta = h.syncNodeMeta(int64(id), agentproto.NodeMeta{City: "上海"})
	if meta.City != "" {
		t.Fatalf("fallback should keep row value, got %+v", meta)
	}

	// 节点行加载失败：回落空权威值。
	closeSQL(t, db)
	if meta := h.syncNodeMeta(int64(id), agentproto.NodeMeta{City: "南京"}); meta.City != "" || meta.ISP != "" {
		t.Fatalf("closed db should fall back empty: %+v", meta)
	}
}
