package link

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// Regression (found in 2026-10-10 dogfooding): the read loop must start before OnConnected --
// hello_ack arrives while OnConnected is blocking on it, so a late read loop would leave the ack
// sitting in a socket buffer nobody reads, and the agent would time out every handshake against the real panel.
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
	// the handshake reply must be delivered by the read loop inside the OnConnected wait window.
	select {
	case env := <-acks:
		if env.Type != agentproto.MsgHelloAck {
			t.Fatalf("first message type = %q, want %q", env.Type, agentproto.MsgHelloAck)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("hello_ack not delivered during handshake")
	}
	// connectOnce must be reclaimed after the disconnect.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("connectOnce did not return after cancel")
	}
}

// fakeHandlers' OnConnected mimics the app handshake semantics: after sending hello it blocks waiting
// for hello_ack (that ack can only be delivered via OnMessage by the read loop).
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

// runHandlers is what Run uses: the handshake returns immediately and server-side disconnects drive the reconnect loop.
type runHandlers struct{ connects, disconnects int64 }

func (r *runHandlers) OnConnected(context.Context, func(agentproto.Envelope) error) error { return nil }
func (r *runHandlers) OnMessage(context.Context, agentproto.Envelope, func(agentproto.Envelope) error) {
}
func (r *runHandlers) OnDisconnected() { atomic.AddInt64(&r.disconnects, 1) }

// echoHandler collects the valid messages the read loop delivers, checking that the drop branch does not hurt later messages.
type echoHandler struct {
	mu  sync.Mutex
	got []string
}

func (e *echoHandler) OnConnected(context.Context, func(agentproto.Envelope) error) error { return nil }
func (e *echoHandler) OnMessage(_ context.Context, env agentproto.Envelope, _ func(agentproto.Envelope) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, env.Type)
}
func (e *echoHandler) OnDisconnected() {}
func (e *echoHandler) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.got...)
}

// wsServer starts a ws endpoint that handles connections per build (the URL is rewritten http -> ws).
func wsServer(t *testing.T, build func(conn *websocket.Conn, n int)) string {
	t.Helper()
	up := websocket.Upgrader{}
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		build(conn, int(atomic.AddInt64(&n, 1))-1)
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[len("http"):]
}

// TestRunReconnectsUntilCancel covers reconnecting with backoff after a disconnect; once the ctx is cancelled Run returns ctx.Err().
func TestRunReconnectsUntilCancel(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn, _ int) {
		// disconnect immediately: the handshake has already succeeded (OnConnected returns nil) and the read loop then errors.
		_ = conn.Close()
	})
	h := &runHandlers{}
	c := New(Options{URL: url, MinBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, h) }()

	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt64(&h.disconnects) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt64(&h.disconnects); got < 3 {
		cancel()
		<-done
		t.Fatalf("disconnects = %d; want >= 3 (reconnect loop)", got)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v; want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestRunCancelledCtxSkipsDial covers an already-cancelled ctx: no dial, immediate return.
func TestRunCancelledCtxSkipsDial(t *testing.T) {
	dialed := int64(0)
	url := wsServer(t, func(conn *websocket.Conn, _ int) {
		atomic.AddInt64(&dialed, 1)
		_ = conn.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := New(Options{URL: url, MinBackoff: 10 * time.Millisecond}).Run(ctx, &runHandlers{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v; want context.Canceled", err)
	}
	if atomic.LoadInt64(&dialed) != 0 {
		t.Fatalf("dialed %d times with cancelled ctx", dialed)
	}
}

// TestRunTLSSetupFailure covers tlsConfig failing before the dial: backoff and retry, no handshake.
func TestRunTLSSetupFailure(t *testing.T) {
	c := New(Options{URL: "ws://127.0.0.1:1/", CAFile: "/nonexistent/ca.pem",
		MinBackoff: 5 * time.Millisecond, MaxBackoff: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx, &runHandlers{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v; want context.DeadlineExceeded", err)
	}
}

// TestReadLoopDropsBadMessages covers bad-JSON and wrong-version messages being dropped while the connection stays up and later messages still arrive.
func TestReadLoopDropsBadMessages(t *testing.T) {
	url := wsServer(t, func(conn *websocket.Conn, _ int) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("{not json"))
		bad := agentproto.Envelope{V: agentproto.ProtocolVersion + 99, ID: "x", Type: "future.type"}
		raw, _ := bad.Marshal()
		_ = conn.WriteMessage(websocket.TextMessage, raw)
		good, _ := agentproto.NewEnvelope("y", agentproto.MsgCalibrateAck, agentproto.CalibrateAck{})
		raw2, _ := good.Marshal()
		_ = conn.WriteMessage(websocket.TextMessage, raw2)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	h := &echoHandler{}
	c := New(Options{URL: url})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = c.connectOnce(ctx, h) }()

	deadline := time.Now().Add(3 * time.Second)
	for len(h.types()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	got := h.types()
	if len(got) != 1 || got[0] != agentproto.MsgCalibrateAck {
		t.Fatalf("delivered = %v; want only %q", got, agentproto.MsgCalibrateAck)
	}
}

// TestTLSConfig covers certificate semantics: system roots by default; a CA can be injected; bad CA/missing files error out;
// mTLS needs cert and key given together.
func TestTLSConfig(t *testing.T) {
	c := New(Options{URL: "wss://panel/"})
	cfg, err := c.tlsConfig()
	if err != nil {
		t.Fatalf("default tlsConfig: %v", err)
	}
	if cfg.RootCAs != nil || len(cfg.Certificates) != 0 {
		t.Fatalf("default should rely on system roots, got %+v", cfg)
	}

	dir := t.TempDir()
	certPEM, keyPEM := selfSignedPair(t, dir)

	ca := New(Options{URL: "wss://panel/", CAFile: certPEM})
	cfg, err = ca.tlsConfig()
	if err != nil {
		t.Fatalf("ca tlsConfig: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("ca_file should populate RootCAs")
	}

	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{CAFile: bad}).tlsConfig(); err == nil ||
		!strings.Contains(err.Error(), "no valid certificate") {
		t.Fatalf("bad ca err = %v; want no valid certificate", err)
	}
	if _, err := New(Options{CAFile: filepath.Join(dir, "absent.pem")}).tlsConfig(); err == nil {
		t.Fatal("missing ca_file should error")
	}

	mtls := New(Options{CertFile: certPEM, KeyFile: keyPEM})
	cfg, err = mtls.tlsConfig()
	if err != nil {
		t.Fatalf("mtls tlsConfig: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("certificates = %d; want 1", len(cfg.Certificates))
	}
	// missing private key: without a pair no client certificate is loaded and no error is raised.
	cfg, err = New(Options{CertFile: certPEM}).tlsConfig()
	if err != nil || len(cfg.Certificates) != 0 {
		t.Fatalf("cert without key = %d certs, %v; want 0, nil", len(cfg.Certificates), err)
	}
}

// selfSignedPair generates self-signed certificate and private key PEM file paths.
func selfSignedPair(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ferry-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
