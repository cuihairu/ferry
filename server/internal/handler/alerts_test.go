package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// TestAgentWSAlarm 覆盖异常告警落库（A-22）：agent 上报 → 落库 →
// 同节点同类别同进程去重刷新 → 列表/详情可查 → 手动处理。
func TestAgentWSAlarm(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "alm-1")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "alm-1", token)

	// 进程崩溃告警 → 落库 active
	al, _ := agentproto.NewEnvelope("a1", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindProcCrash, Severity: agentproto.AlarmSeverityCritical,
		Proc: "xray", Message: "xray 崩溃且拉起失败", At: time.Now(),
	})
	if err := c.WriteJSON(al); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", env.Type)
	}

	var rows []storage.Alert
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query alerts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("alerts = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.NodeID != id || row.Kind != agentproto.AlarmKindProcCrash || row.Proc != "xray" ||
		row.Severity != agentproto.AlarmSeverityCritical || row.State != AlertStateActive ||
		row.Message != "xray 崩溃且拉起失败" {
		t.Fatalf("alert row mismatch: %+v", row)
	}

	// 同节点同类别同进程重复上报 → 刷新消息不新增
	al2, _ := agentproto.NewEnvelope("a2", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindProcCrash, Severity: agentproto.AlarmSeverityCritical,
		Proc: "xray", Message: "xray 再次崩溃", At: time.Now(),
	})
	if err := c.WriteJSON(al2); err != nil {
		t.Fatalf("write alarm2: %v", err)
	}
	_ = readEnv(t, c)
	_ = db.Find(&rows).Error
	if len(rows) != 1 || rows[0].Message != "xray 再次崩溃" {
		t.Fatalf("duplicate alarm should refresh: %v", rows)
	}

	// 证书临期告警（无进程）→ 新增一条
	al3, _ := agentproto.NewEnvelope("a3", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindCertExpiry, Severity: agentproto.AlarmSeverityWarning,
		Message: "证书 hk.example.com 将于 2026-12-01 到期（剩 55 天）", At: time.Now(),
	})
	if err := c.WriteJSON(al3); err != nil {
		t.Fatalf("write alarm3: %v", err)
	}
	_ = readEnv(t, c)

	// 列表：默认全部，新在前
	lrec := doJSON(t, r, "GET", "/api/alerts", nil)
	if lrec.Code != http.StatusOK {
		t.Fatalf("list alerts: %d %s", lrec.Code, lrec.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode alerts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("alerts list = %d, want 2", len(list))
	}
	if list[0]["kind"] != agentproto.AlarmKindCertExpiry {
		t.Fatalf("newest first: %v", list[0])
	}

	// 按节点/状态/类别过滤
	lrec = doJSON(t, r, "GET", fmt.Sprintf("/api/alerts?node_id=%d&state=active&kind=cert_expiry", id), nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["kind"] != agentproto.AlarmKindCertExpiry {
		t.Fatalf("filtered alerts mismatch: %v", list)
	}
	// 非法 state 400
	if rec := doJSON(t, r, "GET", "/api/alerts?state=bad", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state should 400: %d", rec.Code)
	}

	// 手动处理证书告警
	certID := uint(list[0]["id"].(float64))
	rrec := doJSON(t, r, "POST", fmt.Sprintf("/api/alerts/%d/resolve", certID), nil)
	if rrec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rrec.Code, rrec.Body)
	}
	var resolved map[string]any
	_ = json.Unmarshal(rrec.Body.Bytes(), &resolved)
	if resolved["state"] != AlertStateResolved || resolved["resolved_at"] == nil {
		t.Fatalf("resolve response mismatch: %v", resolved)
	}
	// 处理后再查 active 只剩 proc_crash
	lrec = doJSON(t, r, "GET", "/api/alerts?state=active", nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["kind"] != agentproto.AlarmKindProcCrash {
		t.Fatalf("active alerts after resolve: %v", list)
	}
	// 不存在的告警 404
	if rec := doJSON(t, r, "POST", "/api/alerts/9999/resolve", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resolve missing should 404: %d", rec.Code)
	}
}

// waitAlertState 轮询直到告警状态符合期望（WS 处理异步，无 ack 同步点）。
func waitAlertState(t *testing.T, db *gorm.DB, id uint, want string) storage.Alert {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var row storage.Alert
		if err := db.First(&row, id).Error; err == nil && row.State == want {
			return row
		}
		time.Sleep(50 * time.Millisecond)
	}
	var row storage.Alert
	_ = db.First(&row, id)
	t.Fatalf("alert %d state never became %q: %+v", id, want, row)
	return row
}

// TestAgentWSAlarmProcRecover 覆盖进程恢复自动消解（A-22）：
// proc_crash 告警后 agent 上报 running → 告警自动转 resolved。
func TestAgentWSAlarmProcRecover(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, token := createNodeFor(t, r, "alm-2")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "alm-2", token)

	al, _ := agentproto.NewEnvelope("a1", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindProcCrash, Severity: agentproto.AlarmSeverityCritical,
		Proc: "xray", Message: "xray 崩溃且拉起失败", At: time.Now(),
	})
	if err := c.WriteJSON(al); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	_ = readEnv(t, c)

	// 进程恢复运行 → 自动消解
	rep, _ := agentproto.NewEnvelope("p1", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{{Name: "xray", State: "running", Since: time.Now()}},
	})
	if err := c.WriteJSON(rep); err != nil {
		t.Fatalf("write proc report: %v", err)
	}

	var rows []storage.Alert
	_ = db.Find(&rows).Error
	if len(rows) != 1 {
		t.Fatalf("alerts = %d, want 1", len(rows))
	}
	got := waitAlertState(t, db, rows[0].ID, AlertStateResolved)
	if got.ResolvedAt == nil {
		t.Fatalf("resolved alert should carry resolved_at: %+v", got)
	}

	// 其他进程的 running 不影响；stopped 也不消解
	al2, _ := agentproto.NewEnvelope("a2", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindProcCrash, Severity: agentproto.AlarmSeverityCritical,
		Proc: "hysteria2", Message: "hysteria2 崩溃", At: time.Now(),
	})
	if err := c.WriteJSON(al2); err != nil {
		t.Fatalf("write alarm2: %v", err)
	}
	_ = readEnv(t, c)
	rep2, _ := agentproto.NewEnvelope("p2", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{{Name: "hysteria2", State: "stopped", Since: time.Now()}},
	})
	if err := c.WriteJSON(rep2); err != nil {
		t.Fatalf("write proc report2: %v", err)
	}
	_ = db.Find(&rows).Error
	byProc := map[string]storage.Alert{}
	for _, row := range rows {
		byProc[row.Proc] = row
	}
	if byProc["hysteria2"].State != AlertStateActive {
		t.Fatalf("stopped proc must stay active: %+v", byProc["hysteria2"])
	}
}

// TestAgentWSAlarmMultiNode 覆盖多节点告警隔离：两节点同类别告警各自独立。
func TestAgentWSAlarmMultiNode(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	idA, tokenA := createNodeFor(t, r, "alm-a")
	idB, tokenB := createNodeFor(t, r, "alm-b")

	srv := httptest.NewServer(r)
	defer srv.Close()
	ca := fakeAgent(t, srv.URL, "alm-a", tokenA)
	cb := fakeAgent(t, srv.URL, "alm-b", tokenB)

	for i, conn := range []*websocket.Conn{ca, cb} {
		kind := agentproto.AlarmKindHighLoad
		al, _ := agentproto.NewEnvelope(fmt.Sprintf("a%d", i), agentproto.MsgAlarm, agentproto.Alarm{
			Kind: kind, Severity: agentproto.AlarmSeverityWarning,
			Message: "CPU 使用率持续 ≥ 90%", At: time.Now(),
		})
		if err := conn.WriteJSON(al); err != nil {
			t.Fatalf("write alarm: %v", err)
		}
		if env := readEnv(t, conn); env.Type != agentproto.MsgAlarmAck {
			t.Fatalf("expected alarm_ack, got %s", env.Type)
		}
	}

	lrec := doJSON(t, r, "GET", "/api/alerts?kind=high_load", nil)
	var list []map[string]any
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode alerts: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("alerts = %d, want 2 (per node)", len(list))
	}
	seen := map[uint]bool{}
	for _, row := range list {
		seen[uint(row["node_id"].(float64))] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("both nodes should have alerts: %v", seen)
	}
	// 按节点过滤只回本节点
	lrec = doJSON(t, r, "GET", fmt.Sprintf("/api/alerts?node_id=%d", idB), nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || uint(list[0]["node_id"].(float64)) != idB {
		t.Fatalf("node filter mismatch: %v", list)
	}
}
