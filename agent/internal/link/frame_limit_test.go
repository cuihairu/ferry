package link

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// TestReadLoopFrameLimit covers the agent-side inbound frame cap (F-0728): a frame
// over the limit errors the read, drops the connection and the reconnect loop counts
// a disconnect — the agent never buffers an unbounded frame.
func TestReadLoopFrameLimit(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// first message: hello_ack; then slam an oversized frame at the agent.
		ack, _ := agentproto.NewEnvelope("a1", agentproto.MsgHelloAck, agentproto.HelloAck{
			HeartbeatIntervalSec: 30,
		})
		if err := conn.WriteJSON(ack); err != nil {
			return
		}
		big := make([]byte, 1<<20) // 1 MiB, over the test's 4 KiB cap
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteMessage(websocket.BinaryMessage, big); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	url := "ws" + srv.URL[len("http"):]

	h := &runHandlers{}
	cl := New(Options{URL: url})
	cl.opt.MaxFrameBytes = 4 << 10
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cl.Run(ctx, h)
	if atomic.LoadInt64(&h.disconnects) == 0 {
		t.Fatal("oversized frame must drop the connection (at least one disconnect)")
	}
}
