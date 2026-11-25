package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startWSServer starts a wss echo service (the httptest self-signed certificate is written to disk as CAFile),
// and returns the dial address and the CA file path.
func startWSServer(t *testing.T) (addr, caFile string) {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	caFile = filepath.Join(t.TempDir(), "ca.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://"), caFile
}

// TestWSTLSPlugin covers the ws-tls plugin (E-18): the registration is reachable, the handshake sets up the connection,
// small frames echo, streaming echoes across several messages, and reads with no length cap.
func TestWSTLSPlugin(t *testing.T) {
	addr, caFile := startWSServer(t)

	tn, err := Open("ws-tls", Options{CAFile: caFile, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open ws-tls: %v", err)
	}
	if tn.Name() != "ws-tls" {
		t.Fatalf("name = %s", tn.Name())
	}

	conn, err := tn.Dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// small frame echo
	if _, err := conn.Write([]byte("ping-ws")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 7)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping-ws" {
		t.Fatalf("echo = %q", buf)
	}

	// a large payload written in one message and read out across messages with a small 1KB buffer (stream semantics)
	big := make([]byte, 100<<10)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = conn.Write(big)
	}()
	got, err := io.ReadAll(io.LimitReader(conn, int64(len(big))))
	if err != nil {
		t.Fatalf("read big: %v", err)
	}
	if !bytes.Equal(got, big) {
		t.Fatalf("big echo mismatch: got %d bytes", len(got))
	}
}

// TestWSTLSCancel covers cancellation and unreachability: dialing with a pre-cancelled ctx fails immediately.
func TestWSTLSCancel(t *testing.T) {
	addr, caFile := startWSServer(t)
	tn, err := Open("ws-tls", Options{CAFile: caFile, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tn.Dial(ctx, addr); err == nil {
		t.Fatal("cancelled dial should fail")
	}
	// an unreachable port: fail fast rather than hang
	if _, err := tn.Dial(context.Background(), "127.0.0.1:1"); err == nil {
		t.Fatal("refused dial should fail")
	}
}
