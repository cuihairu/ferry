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

// TestSpeedtestRateLimit 覆盖测速字节端点的 IP 限流（30 次/分钟）：第 31
// 次 429。
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

// TestSpeedtestTailChunk 覆盖非 32KB 整倍数的尾块（chunk=remain）。
func TestSpeedtestTailChunk(t *testing.T) {
	r := newTestRouter(t)
	rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65537", nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 65537 {
		t.Fatalf("tail chunk: %d len=%d", rec.Code, rec.Body.Len())
	}
}

// TestSpeedtestAbortClient 覆盖下发途中客户端中断的写失败分支：大包传输
// 中客户端 RST，服务端写错误即止（该分支无外部产物，断言其后服务仍正常）。
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
		_ = tc.SetLinger(0) // 关闭即 RST，服务端后续写必失败
	}
	_ = conn.Close()
	time.Sleep(200 * time.Millisecond)
	if rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil); rec.Code != http.StatusOK {
		t.Fatalf("after abort: %d", rec.Code)
	}
}

// expectNoReply 断言短窗口内没有回帧（读超时即通过）。
func expectNoReply(t *testing.T, c *websocket.Conn) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("unexpected reply")
	}
}

// TestAgentWSCalibrateDBErrors 覆盖测速校准落库失败：节点行已删（定位
// 失败）与节点回写失败两路都不回应答。回写注错用标志位门控的回调，且
// 先于 agent 握手注册（gorm 回调 compile 与服务端 goroutine 并发即数据
// 竞争）。
func TestAgentWSCalibrateDBErrors(t *testing.T) {
	cal := func(id string) agentproto.Envelope {
		env, _ := agentproto.NewEnvelope(id, agentproto.MsgCalibrate, agentproto.Calibrate{
			MeasuredDownMbps: 50, Bytes: 4 << 20, Seconds: 0.64,
		})
		return env
	}

	// 定位失败：握手后删掉节点行，校准定位不到 → 不回应答。
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

	// 回写失败：节点行在，更新被门控回调注错 → 不回应答。
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
