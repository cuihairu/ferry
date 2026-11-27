package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// TestAgentWSAlarm covers abnormal alert persistence (A-22): agent report ->
// stored -> dedup refresh for the same node, kind and process -> list/detail
// queryable -> manual resolve.
func TestAgentWSAlarm(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "alm-1")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "alm-1", token)

	// Process crash alert -> stored as active.
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
	// Activating a crash alert also stores a proc_crashed event (HERALD-3 leftover);
	// WS handling is async, so poll until it lands.
	waitEventCount(t, db, "proc_crashed", 1)

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

	// A repeat report for the same node, kind and process -> the message is
	// refreshed, no new row.
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
	// A repeat report only refreshes: the event is not re-emitted.
	waitEventCount(t, db, "proc_crashed", 1)

	// Cert-expiry alert (no process) -> a new row.
	al3, _ := agentproto.NewEnvelope("a3", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindCertExpiry, Severity: agentproto.AlarmSeverityWarning,
		Message: "证书 hk.example.com 将于 2026-12-01 到期（剩 55 天）", At: time.Now(),
	})
	if err := c.WriteJSON(al3); err != nil {
		t.Fatalf("write alarm3: %v", err)
	}
	_ = readEnv(t, c)

	// List: all by default, newest first.
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

	// Filter by node/state/kind.
	lrec = doJSON(t, r, "GET", fmt.Sprintf("/api/alerts?node_id=%d&state=active&kind=cert_expiry", id), nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["kind"] != agentproto.AlarmKindCertExpiry {
		t.Fatalf("filtered alerts mismatch: %v", list)
	}
	// An invalid state is 400.
	if rec := doJSON(t, r, "GET", "/api/alerts?state=bad", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state should 400: %d", rec.Code)
	}

	// Resolve the cert alert manually.
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
	// After resolving, only proc_crash remains among active alerts.
	lrec = doJSON(t, r, "GET", "/api/alerts?state=active", nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["kind"] != agentproto.AlarmKindProcCrash {
		t.Fatalf("active alerts after resolve: %v", list)
	}
	// A nonexistent alert is 404.
	if rec := doJSON(t, r, "POST", "/api/alerts/9999/resolve", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resolve missing should 404: %d", rec.Code)
	}
}

// waitEventCount polls until the row count of events with a kind reaches the
// target (WS handling is async, with no sync point).
func waitEventCount(t *testing.T, db *gorm.DB, kind string, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		if err := db.Model(&storage.Event{}).Where("kind = ?", kind).Count(&n).Error; err == nil && n == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var n int64
	_ = db.Model(&storage.Event{}).Where("kind = ?", kind).Count(&n)
	t.Fatalf("events kind=%s count = %d, want %d", kind, n, want)
}

// waitAlertState polls until an alert's state matches the expectation (WS
// handling is async, with no ack sync point).
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

// TestAgentWSAlarmProcRecover covers auto-resolution on process recovery
// (A-22): after a proc_crash alert, the agent reports running -> the alert
// turns resolved on its own.
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

	// The process is running again -> auto-resolved.
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

	// Another process being running has no effect; stopped does not resolve either.
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

// TestAgentWSAlarmMultiNode covers alert isolation across nodes: same-kind
// alerts on two nodes stay independent.
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
	// The node filter returns only that node's alerts.
	lrec = doJSON(t, r, "GET", fmt.Sprintf("/api/alerts?node_id=%d", idB), nil)
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 1 || uint(list[0]["node_id"].(float64)) != idB {
		t.Fatalf("node filter mismatch: %v", list)
	}
}

// TestAlertGuards covers the alert plane's fallbacks and filter validation: a
// missing severity defaults to warning, invalid limit/state/node_id are 400,
// a valid limit applies, and an invalid resolve id is 404.
func TestAlertGuards(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "alm-guard")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "alm-guard", token)

	// A report without severity -> stored as warning (recordAlert fallback).
	al, _ := agentproto.NewEnvelope("g1", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindConfigError, Proc: "xray", Message: "配置下发失败", At: time.Now(),
	})
	if err := c.WriteJSON(al); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", env.Type)
	}
	var row storage.Alert
	if err := db.Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if row.Severity != agentproto.AlarmSeverityWarning || row.NodeID != id {
		t.Fatalf("default severity row = %+v", row)
	}

	// List: an invalid or out-of-range limit is 400, a valid limit applies.
	if rec := doJSON(t, r, "GET", "/api/alerts?limit=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/alerts?limit=501", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit range: %d", rec.Code)
	}
	rec := doJSON(t, r, "GET", "/api/alerts?limit=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit 5: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.Alert
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	// An invalid state is 400; an invalid node_id is 400; a valid filter combination hits.
	if rec := doJSON(t, r, "GET", "/api/alerts?state=bogus", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/alerts?node_id=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad node_id: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", fmt.Sprintf("/api/alerts?state=active&kind=config_error&node_id=%d", id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != agentproto.AlarmKindConfigError {
		t.Fatalf("filtered rows = %+v", rows)
	}

	// Manual resolve: an invalid id is 404.
	if rec := doJSON(t, r, "POST", "/api/alerts/abc/resolve", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resolve bad id: %d", rec.Code)
	}
}

// TestConfigErrorAlertHerald covers F-1826: a config_error alarm activates the
// panel alert AND lands one config_error warning in the herald outbox
// (node+proc dedup); a repeat alarm refreshes the alert without a second event
// (activation is the emission gate).
func TestConfigErrorAlertHerald(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "cerr-1")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "cerr-1", token)

	al, _ := agentproto.NewEnvelope("ce1", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindConfigError, Proc: "xray",
		Message: "version v3 apply failed: reload: exit 1 (rolled back)", At: time.Now(),
	})
	if err := c.WriteJSON(al); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", env.Type)
	}
	var ev storage.Event
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := db.Where("kind = ?", herald.KindConfigError).First(&ev).Error
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("config_error event missing: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}
	if ev.DedupKey != fmt.Sprintf("node:%d:config_error:xray", id) {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}

	// A repeat alarm refreshes the active alert; no second event is emitted.
	al2, _ := agentproto.NewEnvelope("ce2", agentproto.MsgAlarm, agentproto.Alarm{
		Kind: agentproto.AlarmKindConfigError, Proc: "xray", Message: "still failing", At: time.Now(),
	})
	if err := c.WriteJSON(al2); err != nil {
		t.Fatalf("write alarm 2: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", env.Type)
	}
	time.Sleep(300 * time.Millisecond)
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindConfigError).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("repeat alarm must not re-emit: %d events", n)
	}
	var row storage.Alert
	if err := db.Where("node_id = ? AND kind = ?", id, agentproto.AlarmKindConfigError).
		Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("alert row: %v", err)
	}
	if row.Message != "still failing" || row.State != AlertStateActive {
		t.Fatalf("alert not refreshed: %+v", row)
	}
}
