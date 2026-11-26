package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testCert signs a self-signed certificate on the spot (test only, never written to a file).
func testCert(t testing.TB) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ferry-test"},
		DNSNames:     []string{"cover.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	rawKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: rawKey}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRegistry(t *testing.T) {
	// plugin names carry a run ordinal: the registry is package-global, so re-running with -count>1 would collide on the same name.
	name := fmt.Sprintf("dup-%d", time.Now().UnixNano())
	if err := Register(name, func(Options) (Tunnel, error) { return nil, nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := Register(name, func(Options) (Tunnel, error) { return nil, nil }); err == nil {
		t.Fatal("duplicate register must fail")
	}
	if _, err := Open("no-such-plugin", Options{}); err == nil {
		t.Fatal("unknown plugin must fail")
	}
	tn, err := Open(DefaultPlugin, Options{})
	if err != nil {
		t.Fatalf("open default: %v", err)
	}
	if tn.Name() != DefaultPlugin {
		t.Fatalf("name = %q", tn.Name())
	}
	found := false
	for _, n := range Names() {
		if n == DefaultPlugin {
			found = true
		}
	}
	if !found {
		t.Fatal("default plugin missing from Names")
	}
}

func TestTLSCamoHandshake(t *testing.T) {
	cert := testCert(t)
	var gotSNI atomic.Value
	var gotALPN atomic.Value
	srv := &tls.Config{
		GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			gotSNI.Store(hi.ServerName)
			gotALPN.Store(hi.SupportedProtos)
			return &cert, nil
		},
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 1)
		_, _ = c.Read(buf) // the handshake completed
	}()

	// the self-signed CA is written to a temp file and used as CAFile (the same path as a private deployment).
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	tn, err := Open(DefaultPlugin, Options{ServerName: "cover.example.com", CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := tn.Dial(ctx, ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, ok := conn.(*tls.Conn); !ok {
		t.Fatalf("not a TLS conn: %T", conn)
	}
	deadline := time.Now().Add(5 * time.Second)
	for gotSNI.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gotSNI.Load() != "cover.example.com" {
		t.Fatalf("SNI = %v", gotSNI.Load())
	}
	protos, _ := gotALPN.Load().([]string)
	if len(protos) == 0 || protos[0] != "h2" {
		t.Fatalf("ALPN = %v", protos)
	}
	if err := tn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestPingPongAlive(t *testing.T) {
	// net.Pipe is a rendezvous (unbuffered); both ends writing at once would deadlock each other.
	// the heartbeat runs on TCP, so test it over loopback TCP.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acceptc := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		acceptc <- c
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var b net.Conn
	select {
	case b = <-acceptc:
	case <-time.After(5 * time.Second):
		t.Fatal("accept timeout")
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- PingPong(ctx, a, 50*time.Millisecond) }()
	go func() { errc <- PingPong(ctx, b, 50*time.Millisecond) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errc:
			if err != nil {
				t.Fatalf("heartbeat: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("heartbeat did not finish on ctx cancel")
		}
	}
}

func TestPingPongDeadPeer(t *testing.T) {
	a, b := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- PingPong(ctx, a, 20*time.Millisecond) }()
	_ = b.Close() // the peer simply vanishes
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("dead peer must return error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no error on dead peer")
	}
}

// TestPluginIdentityAndNoopClose covers the Name and stateless Close rules of the three dial plugins.
func TestPluginIdentityAndNoopClose(t *testing.T) {
	cases := []struct {
		tn   Tunnel
		want string
	}{
		{&sshTunnel{}, pluginSSH},
		{&tlsCamo{}, pluginTLSCamo},
		{&quicSidecar{opts: Options{}}, pluginQUIC},
	}
	for _, c := range cases {
		if got := c.tn.Name(); got != c.want {
			t.Errorf("Name = %q; want %q", got, c.want)
		}
		if err := c.tn.Close(); err != nil {
			t.Errorf("%s Close = %v; want nil", c.want, err)
		}
	}
}

// TestSSHTunnelResetForeignConn covers reset clearing only its own connection: a foreign pointer leaves the state alone.
func TestSSHTunnelResetForeignConn(t *testing.T) {
	tn := &sshTunnel{}
	// sshConn is nil and dead is not: no match, so Close must not fire (otherwise a nil dereference).
	tn.reset(new(ssh.Client))
	if tn.sshConn != nil {
		t.Fatalf("sshConn = %v; want nil", tn.sshConn)
	}
	// closing an empty connection is nil.
	if err := tn.Close(); err != nil {
		t.Fatalf("Close on fresh tunnel = %v", err)
	}
}

// hbPeer starts a TCP heartbeat peer and returns the local connection and a way to stop the peer. TCP instead of
// net.Pipe: the pipe is unbuffered and with both tickers in phase an in-flight ping write would hit a write timeout,
// which belongs to the connection-dead rule and would pollute the ctx cancellation path's assertions.
func hbPeer(t *testing.T, interval time.Duration) (net.Conn, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	peerDone := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer c.Close()
		ctxPeer, cancelPeer := context.WithCancel(context.Background())
		go func() {
			<-stop
			cancelPeer()
		}()
		peerDone <- PingPong(ctxPeer, c, interval)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		close(stop)
		t.Fatal(err)
	}
	return conn, func() {
		close(stop)
		conn.Close()
		select {
		case <-peerDone:
		case <-time.After(3 * time.Second):
			t.Error("peer heartbeat did not exit")
		}
	}
}

// TestServeHeartbeatRoundTrip covers the heartbeat: both ends run PingPong and answer each other, with ctx cancellation returning nil.
func TestServeHeartbeatRoundTrip(t *testing.T) {
	tn := &quicSidecar{opts: Options{}}
	a, stopPeer := hbPeer(t, 20*time.Millisecond)
	defer a.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ownDone := make(chan error, 1)
	go func() { ownDone <- tn.ServeHeartbeat(ctx, a, 20*time.Millisecond) }()

	time.Sleep(150 * time.Millisecond) // a few rounds of ping/pong
	cancel()
	select {
	case err := <-ownDone:
		if err != nil {
			t.Fatalf("ServeHeartbeat after cancel = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ServeHeartbeat did not return after cancel")
	}
	stopPeer()
}

// silentPeer starts a TCP peer that only receives and never replies: it keeps consuming frames (so our writes never block)
// and pings per tick (so our read side never times out) but never answers a ping with a pong -- "connected but not answering".
func silentPeer(t *testing.T, tick time.Duration) net.Conn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	go func() {
		<-stop
		_ = ln.Close()
	}()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		go func() { // the receiving side drops without answering
			buf := make([]byte, frameLen)
			for {
				if _, err := io.ReadFull(c, buf); err != nil {
					return
				}
			}
		}()
		tk := time.NewTicker(tick)
		defer tk.Stop()
		var nonce uint64
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				nonce++
				var out [frameLen]byte
				out[0] = framePing
				binary.BigEndian.PutUint64(out[1:], nonce)
				_ = c.SetWriteDeadline(time.Now().Add(time.Second))
				if _, err := c.Write(out[:]); err != nil {
					return
				}
			}
		}
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		close(stop)
		t.Fatal(err)
	}
	t.Cleanup(func() { close(stop); conn.Close() })
	return conn
}

// TestServeHeartbeatLost covers the dead-link verdict: the connection is alive and the peer keeps pinging (no read timeout),
// but it never answers a pong -- three consecutive periods without a response declare the connection dead (ErrHeartbeatLost).
//
// a fully silent peer would land the read timeout (3*interval) and the third miss at the same moment, making the assertion
// jitter between the two error paths; having the peer only ping and never pong keeps the read-timeout path cleanly out.
func TestServeHeartbeatLost(t *testing.T) {
	a := silentPeer(t, 10*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- (&tlsCamo{}).ServeHeartbeat(context.Background(), a, 20*time.Millisecond) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrHeartbeatLost) {
			t.Fatalf("ServeHeartbeat = %v; want ErrHeartbeatLost", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeHeartbeat did not detect lost connection")
	}
}

// TestPluginServeHeartbeatAndClose covers all four plugins: the heartbeat is a PingPong delegation and Close releases nothing,
// but each must run for real and return no error (the contract surface: every method of the Tunnel interface).
func TestPluginServeHeartbeatAndClose(t *testing.T) {
	plugins := []struct {
		name string
		tn   Tunnel
	}{
		{"tls-camo", &tlsCamo{}},
		{"ws-tls", &wsTls{}},
		{"quic-sidecar", &quicSidecar{opts: Options{}}},
		{"ssh", &sshTunnel{}},
	}
	for _, p := range plugins {
		t.Run(p.name, func(t *testing.T) {
			conn, stopPeer := hbPeer(t, 20*time.Millisecond)
			defer conn.Close()

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- p.tn.ServeHeartbeat(ctx, conn, 20*time.Millisecond) }()
			time.Sleep(100 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("ServeHeartbeat = %v; want nil", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("ServeHeartbeat did not return after cancel")
			}
			stopPeer()

			if err := p.tn.Close(); err != nil {
				t.Fatalf("Close = %v; want nil", err)
			}
		})
	}
}

// TestWSConnSurface covers wsConn's net.Conn adapter surface: addresses and read/write deadlines must pass through to the underlying layer.
func TestWSConnSurface(t *testing.T) {
	addr, caFile := startWSServer(t)
	tn, err := Open("ws-tls", Options{CAFile: caFile, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("open ws-tls: %v", err)
	}
	conn, err := tn.Dial(t.Context(), addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c, ok := conn.(*wsConn)
	if !ok {
		t.Fatalf("conn type = %T; want *wsConn", conn)
	}
	if c.LocalAddr() == nil || c.RemoteAddr() == nil {
		t.Fatal("addr should be delegated to underlying websocket conn")
	}
	if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := c.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if err := c.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	// clear the deadline, otherwise Close could get stuck on it.
	if err := c.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("clear deadline: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
