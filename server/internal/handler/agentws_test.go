package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
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

func TestAgentWSConfigPush(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "cfg-1", "address": "tw.example.com", "port": 443, "protocol": "vless",
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

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "cfg-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 推送配置：请求阻塞等待 ack，agent 侧异步回 config.ack
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/config", id), map[string]any{
			"proc": "xray", "kind": "xray", "payload": `{"inbounds":[]}`,
		})
		done <- rec
	}()

	push := readEnv(t, c)
	if push.Type != agentproto.MsgConfigPush {
		t.Fatalf("expected config_push, got %s", push.Type)
	}
	var cp agentproto.ConfigPush
	if err := push.Decode(&cp); err != nil {
		t.Fatalf("decode config_push: %v", err)
	}
	if cp.Proc != "xray" || cp.Payload != `{"inbounds":[]}` || cp.Sha256 == "" {
		t.Fatalf("config_push fields mismatch: %+v", cp)
	}
	ack, _ := agentproto.NewEnvelope(push.ID, agentproto.MsgConfigAck, agentproto.ConfigAck{
		Proc: cp.Proc, Version: cp.Version, OK: true, Validated: true,
	})
	if err := c.WriteJSON(ack); err != nil {
		t.Fatalf("write config_ack: %v", err)
	}

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("push config: %d %s", rec.Code, rec.Body)
		}
		var row map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &row)
		if row["status"] != "applied" || row["validated"] != true {
			t.Fatalf("node_config row mismatch: %v", row)
		}
		if row["sha256"] != cp.Sha256 {
			t.Fatalf("sha256 mismatch: %v", row["sha256"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("push config timed out")
	}

	// 下发历史可查
	hrec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/configs", id), nil)
	var rows []map[string]any
	if err := json.Unmarshal(hrec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal configs: %v", err)
	}
	if len(rows) != 1 || rows[0]["status"] != "applied" {
		t.Fatalf("config history mismatch: %v", rows)
	}
}

func TestAgentWSConfigPushOffline(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "lone-1", "address": "us.example.com", "port": 443, "protocol": "vless",
	})
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	id := int(node["id"].(float64))

	// 节点未接入：502 且落一条 failed 记录
	rec = doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/config", id), map[string]any{
		"proc": "xray", "kind": "xray", "payload": `{}`,
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline push: %d %s", rec.Code, rec.Body)
	}
	hrec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/configs", id), nil)
	var rows []map[string]any
	_ = json.Unmarshal(hrec.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0]["status"] != "failed" {
		t.Fatalf("offline push must leave failed record: %v", rows)
	}
}

func TestAgentWSTrafficReport(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "traf-1", "address": "jp.example.com", "port": 443, "protocol": "vless",
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

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "traf-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 流量上报 → traffic_ack 回执条数（SAVE-4：xray 行带拦截增量）
	rep, _ := agentproto.NewEnvelope("tr-1", agentproto.MsgTraffic, agentproto.TrafficReport{
		Items: []agentproto.ProcTraffic{
			{Proc: "xray", Rx: 1024, Tx: 2048, BlockedBytes: 512, Conns: 7, At: time.Now()},
			{Proc: "hysteria2", Rx: 1, Tx: 2, Conns: 1, At: time.Now()},
		},
	})
	if err := c.WriteJSON(rep); err != nil {
		t.Fatalf("write traffic report: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack, got %s", env.Type)
	}
	var ack agentproto.TrafficAck
	if err := env.Decode(&ack); err != nil {
		t.Fatalf("decode traffic ack: %v", err)
	}
	if ack.Recorded != 2 {
		t.Fatalf("recorded = %d, want 2", ack.Recorded)
	}

	// 记账可查
	trec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/traffic-logs", id), nil)
	var rows []map[string]any
	if err := json.Unmarshal(trec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal traffic logs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("traffic logs = %d, want 2", len(rows))
	}
	byProc := map[string]map[string]any{}
	for _, row := range rows {
		byProc[row["proc"].(string)] = row
	}
	if byProc["xray"]["rx_bytes"].(float64) != 1024 || byProc["xray"]["conns"].(float64) != 7 {
		t.Fatalf("xray row mismatch: %v", byProc["xray"])
	}
	// 拦截增量随行落库；无拦截采集的行缺省 0
	if byProc["xray"]["blocked_bytes"].(float64) != 512 {
		t.Fatalf("xray blocked_bytes = %v, want 512", byProc["xray"]["blocked_bytes"])
	}
	if byProc["hysteria2"]["blocked_bytes"].(float64) != 0 {
		t.Fatalf("hysteria2 blocked_bytes = %v, want 0", byProc["hysteria2"]["blocked_bytes"])
	}
}

// TestAgentWSUserTraffic 是 P1-3 逐用户映射：per-user 增量按
// users.username=邮箱 落 traffic_logs，未注册邮箱丢弃不造用户。
func TestAgentWSUserTraffic(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	for _, name := range []string{"alice", "bob"} {
		if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": name, "quota_bytes": 1 << 30}); rec.Code != http.StatusCreated {
			t.Fatalf("create user %s: %d %s", name, rec.Code, rec.Body)
		}
	}

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "ut-1", "address": "us.example.com", "port": 443, "protocol": "vless",
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
		Token: token, AgentID: "ut-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	rep, _ := agentproto.NewEnvelope("tr-1", agentproto.MsgTraffic, agentproto.TrafficReport{
		Items: []agentproto.ProcTraffic{
			{
				Proc: "xray", Rx: 300, Tx: 150, At: time.Now(),
				Users: []agentproto.UserTraffic{
					{Email: "alice", Rx: 200, Tx: 100},
					{Email: "bob", Rx: 100, Tx: 50},
					{Email: "ghost", Rx: 999, Tx: 999}, // 未注册：丢弃
				},
			},
		},
	})
	if err := c.WriteJSON(rep); err != nil {
		t.Fatalf("write traffic report: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgTrafficAck {
		t.Fatalf("expected traffic_ack, got %s", env.Type)
	}

	// alice/bob 各落一行，ghost 落库即失败
	var rows []storage.TrafficLog
	if err := db.Order("user_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query traffic logs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("traffic_logs rows = %d, want 2 (ghost 丢弃)", len(rows))
	}
	var users []storage.User
	if err := db.Where("username IN ?", []string{"alice", "bob"}).Find(&users).Error; err != nil || len(users) != 2 {
		t.Fatalf("seed users: %v (n=%d)", err, len(users))
	}
	byUser := map[uint]storage.TrafficLog{}
	for _, row := range rows {
		byUser[row.UserID] = row
		if row.NodeID == nil || *row.NodeID != uint(node["id"].(float64)) {
			t.Fatalf("node_id mismatch: %+v", row)
		}
	}
	if len(byUser) != 2 {
		t.Fatalf("rows should map to 2 users, got %d", len(byUser))
	}
	for _, u := range users {
		row, ok := byUser[u.ID]
		if !ok {
			t.Fatalf("user %s has no traffic row", u.Username)
		}
		wantRx, wantTx := int64(200), int64(100)
		if u.Username == "bob" {
			wantRx, wantTx = int64(100), int64(50)
		}
		if row.RxBytes != wantRx || row.TxBytes != wantTx {
			t.Fatalf("%s row = %d/%d, want %d/%d", u.Username, row.RxBytes, row.TxBytes, wantRx, wantTx)
		}
	}
}
