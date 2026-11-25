package handler

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

// TestSpeedtestRateLimit covers the speed test bytes endpoint's per-IP rate
// limit (30 requests/minute): the 31st gets a 429.
func TestSpeedtestRateLimit(t *testing.T) {
	r := newTestRouter(t)
	for i := 0; i < 30; i++ {
		if rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil); rec.Code != http.StatusOK {
			t.Fatalf("req %d: %d", i+1, rec.Code)
		}
	}
	if rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("31st should 429: %d", rec.Code)
	}
}

// TestSpeedtestTailChunk covers the tail chunk that is not a multiple of 32KB (chunk=remain).
func TestSpeedtestTailChunk(t *testing.T) {
	r := newTestRouter(t)
	rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65537", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 65537 {
		t.Fatalf("tail chunk: %d len=%d", rec.Code, rec.Body.Len())
	}
}

// TestSpeedtestAbortClient covers the write-failure branch when the client aborts mid-delivery:
// the client RSTs during a large transfer and the server stops on the write error (the branch has no external side effects, so only assert the server still works afterwards).
func TestSpeedtestAbortClient(t *testing.T) {
	r := newTestRouter(t)
	srv := httptest.NewServer(r)
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn, "GET /api/speedtest/bytes?n=%d HTTP/1.1\r\nHost: t\r\n\r\n", 8<<20)
	buf := make([]byte, 4096)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("read head: %v", err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0) // closing sends an RST, so the server's later writes must fail
	}
	_ = conn.Close()
	time.Sleep(200 * time.Millisecond)
	if rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil); rec.Code != http.StatusOK {
		t.Fatalf("after abort: %d", rec.Code)
	}
}

// expectNoReply asserts that no reply frame arrives within a short window (a read timeout means pass).
func expectNoReply(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("unexpected reply")
	}
}

// TestAgentWSCalibrateDBErrors covers calibration persistence failures: neither the deleted node row
// (lookup failure) nor the failed node write-back replies with an ack. The write-back fault injection
// uses a flag-gated callback registered before the agent handshake (gorm callback compile concurrent
// with a server goroutine would be a data race).
func TestAgentWSCalibrateDBErrors(t *testing.T) {
	cal := func(id string) agentproto.Envelope {
		env, _ := agentproto.NewEnvelope(id, agentproto.MsgCalibrate, agentproto.Calibrate{
			MeasuredDownMbps: 50, Bytes: 4 << 20, Seconds: 0.64,
		})
		return env
	}

	// lookup failure: delete the node row after the handshake, so calibration cannot locate it -> no ack.
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "cal-gone")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "cal-gone", token)
	if err := db.Delete(&storage.Node{}, id).Error; err != nil {
		t.Fatal(err)
	}
	if err := c.WriteJSON(cal("cal-x")); err != nil {
		t.Fatal(err)
	}
	expectNoReply(t, c)

	// write-back failure: the node row exists but the update is faulted by the gated callback -> no ack.
	r2, db2 := newTestRouterWithDB(t)
	_, token2 := createNodeFor(t, r2, "cal-upd")
	var failUpd atomic.Bool
	db2.Callback().Update().Before("gorm:update").Register("t:calupd", func(tx *gorm.DB) {
		if failUpd.Load() {
			if _, ok := tx.Statement.Model.(*storage.Node); ok {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	srv2 := httptest.NewServer(r2)
	defer srv2.Close()
	c2 := fakeAgent(t, srv2.URL, "cal-upd", token2)
	failUpd.Store(true)
	if err := c2.WriteJSON(cal("cal-y")); err != nil {
		t.Fatal(err)
	}
	expectNoReply(t, c2)
}
