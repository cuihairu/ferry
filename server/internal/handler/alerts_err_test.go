package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// sendAlarm reports one alert from a fake agent and waits for the receipt
// (WS handling is async, so the ack means it is done).
func sendAlarm(t *testing.T, c *websocket.Conn, id string, al agentproto.Alarm) {
	t.Helper()
	env, _ := agentproto.NewEnvelope(id, agentproto.MsgAlarm, al)
	if err := c.WriteJSON(env); err != nil {
		t.Fatalf("write alarm: %v", err)
	}
	if got := readEnv(t, c); got.Type != agentproto.MsgAlarmAck {
		t.Fatalf("expected alarm_ack, got %s", got.Type)
	}
}

// TestRecordAlertDBErrors covers the alert persistence error branches: a failed
// insert only logs, a failed crash-event emit does not block, a failed active
// alert lookup stores nothing, a failed refresh save does not block -- all of
// them still reply with an ack as usual.
func TestRecordAlertDBErrors(t *testing.T) {
	alarm := func() agentproto.Alarm {
		return agentproto.Alarm{Kind: agentproto.AlarmKindProcCrash,
			Severity: agentproto.AlarmSeverityCritical, Proc: "xray", Message: "m", At: time.Now()}
	}

	// Insert failure -> log only, no alert row. The callback is registered before
	// the agent starts: compiling in gorm callback Register racing Execute in the
	// handshake-side goroutine would be a data race.
	r, db := newTestRouterWithDB(t)
	_, token := createNodeFor(t, r, "ale-a")
	db.Callback().Create().Before("gorm:create").Register("t:alcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Alert); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "ale-a", token)
	sendAlarm(t, c, "a1", alarm())
	var n int64
	db.Model(&storage.Alert{}).Count(&n)
	if n != 0 {
		t.Fatalf("create-fail wrote %d alerts", n)
	}

	// Crash-event emit failure -> the alert row stays, the event is not stored.
	r2, db2 := newTestRouterWithDB(t)
	_, token2 := createNodeFor(t, r2, "ale-b")
	db2.Callback().Create().Before("gorm:create").Register("t:alemit", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.Event); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	srv2 := httptest.NewServer(r2)
	defer srv2.Close()
	c2 := fakeAgent(t, srv2.URL, "ale-b", token2)
	sendAlarm(t, c2, "a1", alarm())
	var ev int64
	db2.Model(&storage.Event{}).Where("kind = ?", "proc_crashed").Count(&ev)
	if ev != 0 {
		t.Fatalf("emit-fail wrote %d events", ev)
	}
	var al1 int64
	db2.Model(&storage.Alert{}).Count(&al1)
	if al1 != 1 {
		t.Fatalf("alert row = %d, want 1", al1)
	}

	// Active alert lookup failure -> neither stored nor refreshed.
	r3, db3 := newTestRouterWithDB(t)
	_, token3 := createNodeFor(t, r3, "ale-c")
	regQueryFail("t:alfind", db3, (*storage.Alert)(nil))
	srv3 := httptest.NewServer(r3)
	defer srv3.Close()
	c3 := fakeAgent(t, srv3.URL, "ale-c", token3)
	sendAlarm(t, c3, "a1", alarm())
	var n3 int64
	db3.Model(&storage.Alert{}).Count(&n3)
	if n3 != 0 {
		t.Fatalf("find-fail wrote %d alerts", n3)
	}

	// Refresh save failure -> log only, row content unchanged.
	r4, db4 := newTestRouterWithDB(t)
	id4, token4 := createNodeFor(t, r4, "ale-d")
	row := storage.Alert{NodeID: id4, Kind: agentproto.AlarmKindProcCrash,
		Severity: agentproto.AlarmSeverityWarning, Proc: "xray", Message: "old",
		State: AlertStateActive}
	if err := db4.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	db4.Callback().Update().Before("gorm:update").Register("t:alsave", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Alert); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	srv4 := httptest.NewServer(r4)
	defer srv4.Close()
	c4 := fakeAgent(t, srv4.URL, "ale-d", token4)
	sendAlarm(t, c4, "a1", alarm())
	var got storage.Alert
	if err := db4.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Message != "old" {
		t.Fatalf("refresh leaked: %+v", got)
	}
}

// TestResolveProcCrashAlertError covers a resolve failure on process recovery
// being logged only (it does not block the report path): reaching the resolve
// callback counts as a hit.
func TestResolveProcCrashAlertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "ale-r")
	row := storage.Alert{NodeID: id, Kind: agentproto.AlarmKindProcCrash,
		Severity: agentproto.AlarmSeverityCritical, Proc: "xray", Message: "m",
		State: AlertStateActive}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	// The callback is registered before the agent handshake (compiling in
	// Register racing Execute in the server-side goroutine would be a data race).
	var mu sync.Mutex
	hits := 0
	db.Callback().Update().Before("gorm:update").Register("t:alresolve", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Alert); ok {
			mu.Lock()
			hits++
			mu.Unlock()
			tx.AddError(errors.New("db down"))
		}
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "ale-r", token)

	pr, _ := agentproto.NewEnvelope("p1", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{{Name: "xray", State: "running"}},
	})
	if err := c.WriteJSON(pr); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := hits
		mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resolve update never fired")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Resolve failure: the row is still active.
	var got storage.Alert
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != AlertStateActive {
		t.Fatalf("state = %s, want active", got.State)
	}
}

// TestAlertsAPIDBErrors covers the alert list and manual-resolve error surface:
// a failed list fetch is 500, a failed resolve write is 500, a failed
// read-back after resolving is 500.
func TestAlertsAPIDBErrors(t *testing.T) {
	// List fetch failure -> 500.
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:allist", db, (*[]storage.Alert)(nil))
	if rec := doJSON(t, r, "GET", "/api/alerts", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	// Resolve write failure -> 500.
	r2, db2 := newTestRouterWithDB(t)
	db2.Callback().Update().Before("gorm:update").Register("t:alwrite", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Alert); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r2, "POST", "/api/alerts/1/resolve", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("resolve write: %d %s", rec.Code, rec.Body)
	}

	// Read-back failure after a successful resolve -> 500.
	r3, db3 := newTestRouterWithDB(t)
	if err := db3.Create(&storage.Alert{NodeID: 1, Kind: "disk", Severity: "warning",
		Proc: "xray", Message: "m", State: AlertStateActive}).Error; err != nil {
		t.Fatal(err)
	}
	regQueryFail("t:alread", db3, (*storage.Alert)(nil))
	if rec := doJSON(t, r3, "POST", "/api/alerts/1/resolve", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("resolve readback: %d %s", rec.Code, rec.Body)
	}
}
