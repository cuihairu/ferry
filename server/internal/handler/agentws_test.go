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

func TestAgentWSHelloMeta(t *testing.T) {
	r := newTestRouter(t)

	// 造一个节点拿接入令牌
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "entry-1", "address": "sh.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	id := int(node["id"].(float64))

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// 首次注册：hello 带元数据初值，hello_ack 应回传
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "entry-1", Version: "test",
		Meta: agentproto.NodeMeta{
			Role: agentproto.RoleEntry, Direction: agentproto.DirectionOut,
			LineType: "cn2_gia", Region: "华东", City: "上海", Datacenter: "sh-1",
			ISP: "电信", Labels: []string{"bgp"}, Transport: "tls",
			BillingType: "按流量", TrafficPriceCents: 120, MonthlyCostCents: 5000,
			BwUpMbps: 100, BwDownMbps: 200, RateLimited: true,
		},
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}
	var ack agentproto.HelloAck
	if err := env.Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.Meta.Role != agentproto.RoleEntry || ack.Meta.Region != "华东" ||
		ack.Meta.ISP != "电信" || ack.Meta.LineType != "cn2_gia" ||
		ack.Meta.City != "上海" || ack.Meta.Datacenter != "sh-1" ||
		ack.Meta.BillingType != "按流量" || ack.Meta.BwUpMbps != 100 ||
		ack.Meta.BwDownMbps != 200 || ack.Meta.TrafficPriceCents != 120 {
		t.Fatalf("ack meta mismatch: %+v", ack.Meta)
	}
	if len(ack.Meta.Labels) != 1 || ack.Meta.Labels[0] != "bgp" {
		t.Fatalf("labels mismatch: %v", ack.Meta.Labels)
	}

	// 节点详情应带上元数据
	drec := doJSON(t, r, "GET", "/api/nodes/"+strconv.Itoa(id), nil)
	var detail map[string]any
	_ = json.Unmarshal(drec.Body.Bytes(), &detail)
	if detail["role"] != "entry" || detail["region"] != "华东" || detail["isp"] != "电信" ||
		detail["line_type"] != "cn2_gia" || detail["billing_type"] != "按流量" {
		t.Fatalf("node detail meta mismatch: %v", detail)
	}

	// 二次注册：面板为权威，agent 的新上报不覆盖，hello_ack 回面板值
	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	defer c2.Close()
	hello2, _ := agentproto.NewEnvelope("h2", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "entry-1", Version: "test",
		Meta: agentproto.NodeMeta{Region: "华南", ISP: "联通", BwUpMbps: 1, BwDownMbps: 1},
	})
	if err := c2.WriteJSON(hello2); err != nil {
		t.Fatalf("write hello2: %v", err)
	}
	env2 := readEnv(t, c2)
	var ack2 agentproto.HelloAck
	if err := env2.Decode(&ack2); err != nil {
		t.Fatalf("decode ack2: %v", err)
	}
	if ack2.Meta.Region != "华东" || ack2.Meta.ISP != "电信" || ack2.Meta.BwUpMbps != 100 {
		t.Fatalf("panel-authoritative meta must win: %+v", ack2.Meta)
	}
}

func TestAgentWSProbeReport(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "probe-1", "address": "sh.example.com", "port": 443, "protocol": "vless",
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
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "probe-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 探测结论上报 → probe.ack 回执条数
	batch, _ := agentproto.NewEnvelope("pr-1", agentproto.MsgProbeReport, agentproto.ProbeReportBatch{
		Items: []agentproto.ProbeReport{
			{
				TargetKind: agentproto.ProbeTargetTunnel, TargetNode: 2,
				Direction: agentproto.DirectionOut, RttMs: 48,
				Reachable: true, Verdict: agentproto.ProbeVerdictHealthy,
				Region: "华东", ISP: "电信", ProbedAt: time.Now(),
			},
		},
	})
	if err := c.WriteJSON(batch); err != nil {
		t.Fatalf("write probe report: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgProbeAck {
		t.Fatalf("expected probe_ack, got %s", env.Type)
	}
	var ack agentproto.ProbeAck
	if err := env.Decode(&ack); err != nil {
		t.Fatalf("decode probe ack: %v", err)
	}
	if ack.Recorded != 1 {
		t.Fatalf("recorded = %d, want 1", ack.Recorded)
	}
}

func TestAgentWSProbeReportResolve(t *testing.T) {
	r := newTestRouter(t)

	// 目标节点（落地）：探测目标按地址解析到该节点
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "landing-1", "address": "sg.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create landing node: %d %s", rec.Code, rec.Body)
	}
	var landing map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &landing)
	landingID := landing["id"].(float64)

	// 探测者节点（入口）
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "entry-1", "address": "sh.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create entry node: %d %s", rec.Code, rec.Body)
	}
	var entry map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &entry)
	token := entry["token"].(string)

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "entry-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 按地址上报探测结论（不带目标节点 ID）
	batch, _ := agentproto.NewEnvelope("pr-1", agentproto.MsgProbeReport, agentproto.ProbeReportBatch{
		Items: []agentproto.ProbeReport{{
			TargetKind: agentproto.ProbeTargetTunnel,
			TargetHost: "sg.example.com:443",
			Direction:  agentproto.DirectionOut,
			RttMs:      88, Reachable: true,
			Verdict:  agentproto.ProbeVerdictHealthy,
			ProbedAt: time.Now(),
		}},
	})
	if err := c.WriteJSON(batch); err != nil {
		t.Fatalf("write probe report: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgProbeAck {
		t.Fatalf("expected probe_ack, got %s", env.Type)
	}

	// 结论应解析到目标节点并补全区域/运营商快照
	lrec := doJSON(t, r, "GET", "/api/probe-reports", nil)
	var reports []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &reports); err != nil {
		t.Fatalf("unmarshal reports: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	got := reports[0]
	if got["target_node_id"].(float64) != landingID {
		t.Fatalf("target_node_id = %v, want %v", got["target_node_id"], landingID)
	}
	if got["region"] != "未知" || got["isp"] != "未知" {
		t.Fatalf("region/isp snapshot: %v/%v", got["region"], got["isp"])
	}
	if got["rtt_ms"].(float64) != 88 {
		t.Fatalf("rtt = %v, want 88", got["rtt_ms"])
	}
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
