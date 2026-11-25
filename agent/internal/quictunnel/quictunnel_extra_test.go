package quictunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// TestParseConnectLineEdgeBranches covers more parsing edges: an empty target address errors, a target without a port
// becomes the SNI as a whole, and an empty sni= value is ignored and falls back to the host.
func TestParseConnectLineEdgeBranches(t *testing.T) {
	if _, _, err := ParseConnectLine("CONNECT \n"); err == nil || !strings.Contains(err.Error(), "missing target address") {
		t.Fatalf("err = %v; want missing target address", err)
	}
	addr, sni, err := ParseConnectLine("CONNECT example.com\n")
	if err != nil || addr != "example.com" || sni != "example.com" {
		t.Fatalf("no-port target = %q/%q err %v; want host as sni", addr, sni, err)
	}
	addr, sni, err = ParseConnectLine("CONNECT a.example.com:8443 sni= sni=real.example.com\n")
	if err != nil || sni != "real.example.com" {
		t.Fatalf("empty sni= ignored: %q err %v; want real.example.com", sni, err)
	}
}

// TestReadConnectLineError covers the peer closing without sending a request line: the read error propagates as is.
func TestReadConnectLineError(t *testing.T) {
	a, b := net.Pipe()
	_ = b.Close()
	if _, _, err := readConnectLine(a, time.Second); err == nil {
		t.Fatal("closed peer must fail readConnectLine")
	}
	_ = a.Close()
}

// TestServeAcceptErrorReturns covers the listener going bad while the ctx is still alive: Serve returns that error
// instead of swallowing it.
func TestServeAcceptErrorReturns(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // close first: Accept errors immediately and the ctx is not cancelled

	c := NewClient(&tls.Config{})
	err = c.Serve(context.Background(), ln, nil)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want listener error while ctx alive", err)
	}
}

// TestServeOneReadConnectError covers a local connection dropping without sending CONNECT: serveOne reports
// a read-connect error which Serve logs (one failure does not take the service down).
func TestServeOneReadConnectError(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	_, pool := selfSignedTLSPair(t)
	c := NewClient(&tls.Config{RootCAs: pool})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- c.Serve(ctx, ln, logger) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close() // closes without sending a request line

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "read connect") {
		if time.Now().After(deadline) {
			t.Fatalf("read connect error not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-serveErr:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

// TestOpenStreamStaleCacheRecovers covers a stale cached connection (the peer having discarded it, simulated locally):
// an open-stream failure clears the cache, redials, joins successfully, and the new connection re-enters the cache.
func TestOpenStreamStaleCacheRecovers(t *testing.T) {
	cert, pool := selfSignedTLSPair(t)
	target := startEchoTarget(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeServer(ctx, qln, target, nil) }()

	c := NewClient(&tls.Config{RootCAs: pool})
	addr := udp.LocalAddr().String()
	key := addr + "|127.0.0.1"
	stale, err := c.dial(ctx, addr, "127.0.0.1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = stale.CloseWithError(0, "simulated stale") // the connection is broken locally while the cache still points at it

	st, err := c.openStream(ctx, addr, "127.0.0.1") // stale open-stream -> drop -> redial
	if err != nil {
		t.Fatalf("openStream after stale: %v", err)
	}
	fresh := c.get(key)
	if fresh == nil || fresh == stale {
		t.Fatal("cache must hold a new conn after stale drop")
	}

	// join verification: the redialed connection is forwarded by the server to the echo landing (write side kept open, echo read back)
	if _, err := st.Write([]byte("roundtrip")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = st.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len("roundtrip"))
	if _, err := io.ReadFull(st, got); err != nil || string(got) != "roundtrip" {
		t.Fatalf("echo = %q err %v; want roundtrip", got, err)
	}

	// the cache-reuse path: the second open stream goes straight through the cached healthy connection without redialing
	again, err := c.openStream(ctx, addr, "127.0.0.1")
	if err != nil {
		t.Fatalf("cached openStream: %v", err)
	}
	if c.get(key) != fresh {
		t.Fatal("cached conn must be reused, not redialed")
	}
	_ = again.Close()
}

// TestServeServerAcceptErrorReturns covers the QUIC listener going bad while the ctx is still alive:
// ServeServer returns that error.
func TestServeServerAcceptErrorReturns(t *testing.T) {
	cert, _ := selfSignedTLSPair(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.Close() // close the transport first: Accept errors immediately and the ctx is not cancelled

	err = ServeServer(context.Background(), qln, "127.0.0.1:1", nil)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v; want listener error while ctx alive", err)
	}
}

// TestForwardStreamDialFailureLogs covers a stream established while the landing target is unreachable: the stream is closed and
// a dial-target line is logged (other streams are unaffected).
func TestForwardStreamDialFailureLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	cert, pool := selfSignedTLSPair(t)
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()
	qln, err := tr.Listen(&tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNIdent},
	}, DefaultQUICConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // hand-rolled server side: forward each accepted stream to an unreachable port
		qc, err := qln.Accept(ctx)
		if err != nil {
			return
		}
		st, err := qc.AcceptStream(ctx)
		if err != nil {
			return
		}
		forwardStream(st, "127.0.0.1:1", logger)
	}()

	c := NewClient(&tls.Config{RootCAs: pool})
	qc, err := c.dial(ctx, udp.LocalAddr().String(), "127.0.0.1")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = qc.CloseWithError(0, "done") }()
	st, err := qc.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if _, err := st.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(buf.String(), "dial target 127.0.0.1:1") {
		if time.Now().After(deadline) {
			t.Fatalf("dial failure not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
