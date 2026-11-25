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

// TestRunDialErrorReconnectsUntilCancel covers an unreachable panel: the dial failure and backoff are logged,
// and the backoff loop retries until the ctx is cancelled.
func TestRunDialErrorReconnectsUntilCancel(t *testing.T) {
	var buf bytes.Buffer
	c := New(Options{
		URL:        "ws://127.0.0.1:1/agent/ws", // refuse the connection
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

// cancelOnConnectedHandlers cancels the ctx and returns an error inside OnConnected: it simulates the
// connection being torn down mid-handshake (connectOnce returns with the ctx already cancelled, so Run should propagate it as is instead of backing off).
type cancelOnConnectedHandlers struct{ cancel context.CancelFunc }

func (h *cancelOnConnectedHandlers) OnConnected(context.Context, func(agentproto.Envelope) error) error {
	h.cancel()
	return errors.New("hello rejected")
}

func (h *cancelOnConnectedHandlers) OnMessage(context.Context, agentproto.Envelope, func(agentproto.Envelope) error) {
}

func (h *cancelOnConnectedHandlers) OnDisconnected() {}

// TestRunCancelDuringConnectOnce covers the ctx being cancelled within the connection lifetime: after connectOnce
// returns, Run returns ctx.Err() as is instead of continuing the reconnect loop.
func TestRunCancelDuringConnectOnce(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn, _ int) {
		for { // run alongside until the client disconnects on its own
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

// TestTLSKeyPairLoadError covers an unreadable mTLS certificate pair file: assembly fails before dialing.
func TestTLSKeyPairLoadError(t *testing.T) {
	c := New(Options{CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"})
	if _, err := c.tlsConfig(); err == nil {
		t.Fatal("missing key pair must error")
	}
}

// TestSenderMarshalError covers a payload that is not valid JSON: the sender errors out and never touches the connection.
func TestSenderMarshalError(t *testing.T) {
	send := newSender(nil) // marshal fails first, so the connection is never used
	err := send(agentproto.Envelope{V: 1, ID: "x", Type: "probe.report",
		Payload: json.RawMessage("{oops")})
	if err == nil {
		t.Fatal("invalid payload must error on marshal")
	}
}
