package link

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// TestRunDialErrorReconnectsUntilCancel 面板不可达：记连接失败与退避日志，
// 按退避循环重试直到 ctx 取消。
func TestRunDialErrorReconnectsUntilCancel(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{
		URL:        "ws://127.0.0.1:1/agent/ws", // 拒绝连接
		MinBackoff: 5 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		Log:        log.New(&buf, "", 0),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx, &runHandlers{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v; want context.DeadlineExceeded", err)
	}
	if !strings.Contains(buf.String(), "connection ended") {
		t.Fatalf("dial failure not logged: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "reconnect in") {
		t.Fatalf("backoff not logged: %q", buf.String())
	}
}

// cancelOnConnectedHandlers 在 OnConnected 里取消 ctx 并报错：模拟握手
// 期间连接被撤（connectOnce 返回时 ctx 已取消，Run 应原样上抛不再退避）。
type cancelOnConnectedHandlers struct{ cancel context.CancelFunc }

func (h *cancelOnConnectedHandlers) OnConnected(context.Context, func(agentproto.Envelope) error) error {
	h.cancel()
	return errors.New("hello rejected")
}

func (h *cancelOnConnectedHandlers) OnMessage(context.Context, agentproto.Envelope, func(agentproto.Envelope) error) {
}

func (h *cancelOnConnectedHandlers) OnDisconnected() {}

// TestRunCancelDuringConnectOnce ctx 在连接生命周期内取消：connectOnce
// 返回后 Run 原样返回 ctx.Err()，而不是继续重连循环。
func TestRunCancelDuringConnectOnce(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn, _ int) {
		for { // 陪跑到客户端主动断开
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	c := New(Options{URL: url, MinBackoff: 5 * time.Millisecond, MaxBackoff: 5 * time.Millisecond,
		Log: log.New(&bytes.Buffer{}, "", 0)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := c.Run(ctx, &cancelOnConnectedHandlers{cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v; want context.Canceled", err)
	}
}

// TestTLSKeyPairLoadError mTLS 证书对文件打不开：装配失败先于拨号。
func TestTLSKeyPairLoadError(t *testing.T) {
	c := New(Options{CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"})
	if _, err := c.tlsConfig(); err == nil {
		t.Fatal("missing key pair must error")
	}
}

// TestSenderMarshalError 载荷不是合法 JSON：发送口报错且不触碰连接。
func TestSenderMarshalError(t *testing.T) {
	send := newSender(nil) // marshal 先失败，连接不会被用到
	err := send(agentproto.Envelope{V: 1, ID: "x", Type: "probe.report",
		Payload: json.RawMessage("{oops")})
	if err == nil {
		t.Fatal("invalid payload must error on marshal")
	}
}
