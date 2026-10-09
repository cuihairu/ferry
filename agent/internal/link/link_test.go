package link

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// 回归（2026-10-10 dogfood 发现）：读循环必须先于 OnConnected 启动——
// hello_ack 在 OnConnected 阻塞等待期间到达，后启动读循环会让 ack 落在
// 无人读的 socket 缓冲里，agent 对真实面板永远握手超时。
func TestConnectOnceDeliversHelloAckDuringHandshake(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var env agentproto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		if env.Type != agentproto.MsgHello {
			return
		}
		ack, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgHelloAck, agentproto.HelloAck{
			HeartbeatIntervalSec: 30,
		})
		if err := conn.WriteJSON(ack); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	url := "ws" + srv.URL[len("http"):]

	acks := make(chan agentproto.Envelope, 1)
	h := fakeHandlers{acks: acks}
	c := New(Options{URL: url, MinBackoff: time.Second, MaxBackoff: time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.connectOnce(ctx, h) }()
	// 握手应答必须在 OnConnected 等待窗口内经读循环送达。
	select {
	case env := <-acks:
		if env.Type != agentproto.MsgHelloAck {
			t.Fatalf("first message type = %q, want %q", env.Type, agentproto.MsgHelloAck)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("hello_ack not delivered during handshake")
	}
	// 断开后 connectOnce 应回收。
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connectOnce did not return after cancel")
	}
}

// fakeHandlers 的 OnConnected 模拟 app 的握手语义：发 hello 后阻塞等
// hello_ack（该 ack 只能经 OnMessage 由读循环送达）。
type fakeHandlers struct{ acks chan agentproto.Envelope }

func (f fakeHandlers) OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error {
	env, _ := agentproto.NewEnvelope("hello", agentproto.MsgHello, agentproto.Hello{Token: "t"})
	if err := send(env); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case env := <-f.acks:
		if env.Type != agentproto.MsgHelloAck {
			return errors.New("unexpected message during handshake")
		}
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("hello ack timeout")
	}
}

func (f fakeHandlers) OnMessage(_ context.Context, env agentproto.Envelope, _ func(agentproto.Envelope) error) {
	select {
	case f.acks <- env:
	default:
	}
}

func (f fakeHandlers) OnDisconnected() {}
