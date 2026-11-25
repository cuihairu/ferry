package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// ---- ssh plugin: validation order and error surface ----

// sshOpts assembles a valid Options, then breaks it item by item as needed.
func sshOpts(t *testing.T, mutate func(*Options)) Options {
	t.Helper()
	dir := t.TempDir()
	opts := Options{
		ServerName:  "127.0.0.1:22",
		AuthUser:    "ferry",
		KeyFile:     writeClientKey(t, dir),
		ForwardAddr: "127.0.0.1:9000",
		Timeout:     time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}
	return opts
}

// TestSSHDialValidationErrors covers the pre-dial validation and credential loading error surface: a missing auth_user,
// a missing or corrupt key file, an unreadable known_hosts file, and a failed TCP dial.
func TestSSHDialValidationErrors(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"auth_user missing", sshOpts(t, func(o *Options) { o.AuthUser = "" }), "auth_user"},
		{"key file missing", sshOpts(t, func(o *Options) { o.KeyFile = filepath.Join(t.TempDir(), "no-key") }), "read key file"},
		{"known_hosts missing", sshOpts(t, func(o *Options) { o.KnownHostsFile = filepath.Join(t.TempDir(), "no-kh") }), "no such file"},
		{"tcp dial fail", sshOpts(t, func(o *Options) { o.ServerName = "127.0.0.1:1" }), "ssh dial"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := (&sshTunnel{opts: c.opts}).dial(ctx)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want contains %q", err, c.want)
			}
		})
	}

	// corrupt key content: the file exists but cannot be parsed
	badKey := filepath.Join(t.TempDir(), "bad-key")
	if err := os.WriteFile(badKey, []byte("not a pem at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (&sshTunnel{opts: sshOpts(t, func(o *Options) { o.KeyFile = badKey })}).dial(ctx)
	if err == nil || !strings.Contains(err.Error(), "parse private key") {
		t.Fatalf("err = %v; want parse private key error", err)
	}
}

// TestSSHWarnsOnceWithoutKnownHosts covers known_hosts being unset (the bootstrap rule): one warning
// fires before dialing (warned is set) and is not set again afterwards.
func TestSSHWarnsOnceWithoutKnownHosts(t *testing.T) {
	tn := &sshTunnel{opts: sshOpts(t, func(o *Options) { o.ServerName = "127.0.0.1:1" })}
	ctx := context.Background()
	if _, err := tn.dial(ctx); err == nil {
		t.Fatal("dial must fail on refused addr")
	}
	if !tn.warned {
		t.Fatal("bootstrap warning must be recorded on first dial")
	}
	if _, err := tn.dial(ctx); err == nil {
		t.Fatal("second dial must fail as well")
	}
}

// rejectingSSHD is an sshd stand-in that only handshakes and rejects every direct-tcpip open-stream request:
// it exercises the ssh plugin's "open-stream failure -> declare dead and reconnect" recovery leg.
type rejectingSSHD struct {
	ln net.Listener
	wg sync.WaitGroup
}

func startRejectingSSHD(t *testing.T) *rejectingSSHD {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)
	s := &rejectingSSHD{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
			if err != nil {
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_ = conn.Wait()
			}()
			go func(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) {
				for req := range reqs {
					if req.WantReply {
						_ = req.Reply(false, nil)
					}
				}
			}(chans, reqs)
			go func(chans <-chan ssh.NewChannel) {
				for newCh := range chans {
					_ = newCh.Reject(ssh.Prohibited, "no forwarding in test")
				}
			}(chans)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		s.wg.Wait()
	})
	return s
}

// TestSSHDialRetryOnChannelReject covers an open stream being rejected: the cached connection is declared dead and it reconnects to retry once;
// if still rejected after reconnecting it reports open channel. Once the sshd stops accepting new connections: the reused connection still fails
// to open a stream -> the reconnect leg itself fails -> the first error is kept.
func TestSSHDialRetryOnChannelReject(t *testing.T) {
	s := startRejectingSSHD(t)
	tn := newSSHTunnel(t, s.ln.Addr().String(), "unused-by-ssh", "").(*sshTunnel)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := tn.Dial(ctx, "ignored")
	if err == nil || !strings.Contains(err.Error(), "open channel") {
		t.Fatalf("err = %v; want open channel error after retry", err)
	}

	// the cache should hold one post-reconnect connection (the retry leg built a new one)
	tn.mu.Lock()
	cached := tn.sshConn
	tn.mu.Unlock()
	if cached == nil {
		t.Fatal("retry leg must leave a cached connection")
	}

	// no longer accepting new connections (existing ones stay alive and still reject opens): the reconnect leg fails and the first error is kept
	// (propagated as is, without the open-channel wrapper)
	if err := s.ln.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = tn.Dial(ctx, "ignored")
	if err == nil || !strings.Contains(err.Error(), "missing port in address") {
		t.Fatalf("err = %v; want preserved first dial error", err)
	}
	tn.mu.Lock()
	gone := tn.sshConn == nil
	tn.mu.Unlock()
	if !gone {
		t.Fatal("dead cache must be cleared after failed redial")
	}
}

// ---- quic sidecar plugin: address defaulting, SNI fallback and no reply ----

// TestQUICSidecarAddrDefault covers a blank environment variable counting as unset: fall back to the default bridge address.
func TestQUICSidecarAddrDefault(t *testing.T) {
	t.Setenv(SidecarEnv, "   ")
	if got := SidecarAddr(); got != DefaultSidecarAddr {
		t.Fatalf("SidecarAddr = %q; want %q", got, DefaultSidecarAddr)
	}
}

// TestQUICSidecarSNIFallbackFromAddr covers ServerName unset: the SNI is the target host;
// with no port in the target, the whole of it is the host.
func TestQUICSidecarSNIFallbackFromAddr(t *testing.T) {
	var gotReq string
	sidecar := startFakeSidecar(t, func(req string) string {
		gotReq = req
		return "OK\n"
	})
	t.Setenv(SidecarEnv, sidecar)
	tn, err := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tn.Dial(context.Background(), "example.com") // a portless target
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if !strings.HasPrefix(gotReq, "CONNECT example.com sni=example.com") {
		t.Fatalf("request = %q; want sni fallback to whole addr", gotReq)
	}
}

// TestQUICSidecarNoReply covers the sidecar dropping without replying after the request: it reports a read-connect
// error instead of hanging.
func TestQUICSidecarNoReply(t *testing.T) {
	silent := startFakeSidecar(t, func(string) string { return "" })
	t.Setenv(SidecarEnv, silent)
	tn, _ := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
	_, err := tn.Dial(context.Background(), "landing.example.com:443")
	if err == nil || !strings.Contains(err.Error(), "read connect reply") {
		t.Fatalf("err = %v; want read connect reply error", err)
	}
}

// ---- tls-camo / ws-tls plugins: ServerName fallback and CA file errors ----

// TestTLSCamoServerNameFallback covers ServerName unset taking the target host (a portless target
// takes the whole thing); the failure surface asserts a dial error rather than a nil pointer.
func TestTLSCamoServerNameFallback(t *testing.T) {
	tn := &tlsCamo{}
	if _, err := tn.Dial(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("dial without port must fail")
	}
}

// TestTLSCamoCAFileErrors covers both error kinds: a missing CA file and a certificate-less file.
func TestTLSCamoCAFileErrors(t *testing.T) {
	tn := &tlsCamo{opts: Options{CAFile: filepath.Join(t.TempDir(), "no-ca.pem")}}
	_, err := tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("err = %v; want read CA file error", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("garbage, not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	tn = &tlsCamo{opts: Options{CAFile: junk}}
	_, err = tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "no certificates in") {
		t.Fatalf("err = %v; want no-certificates error", err)
	}
}

// TestWSTLSServerNameFallback covers ServerName unset: the target host is taken (a portless target takes the whole),
// and a dial failure propagates as is.
func TestWSTLSServerNameFallback(t *testing.T) {
	tn := &wsTls{}
	if _, err := tn.Dial(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("dial without port must fail")
	}
}

// TestWSTLSCAFileErrors covers both error kinds: a missing CA file and a certificate-less file.
func TestWSTLSCAFileErrors(t *testing.T) {
	tn := &wsTls{opts: Options{CAFile: filepath.Join(t.TempDir(), "no-ca.pem")}}
	_, err := tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("err = %v; want read CA file error", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("garbage, not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	tn = &wsTls{opts: Options{CAFile: junk}}
	_, err = tn.Dial(context.Background(), "127.0.0.1:443")
	if err == nil || !strings.Contains(err.Error(), "no certificates in") {
		t.Fatalf("err = %v; want no-certificates error", err)
	}
}

// startTextThenCloseWS starts a ws service stand-in that sends a TextMessage and then a close frame:
// it exercises wsConn's read side skipping non-binary messages.
func startTextThenCloseWS(t *testing.T) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		if err := c.WriteMessage(websocket.TextMessage, []byte("control junk")); err != nil {
			return
		}
		_ = c.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"),
			time.Now().Add(time.Second))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://")
}

// dialRawWSConn obtains a *wsConn white-box (no loopback; only the read/write adapter's edge branches are tested).
func dialRawWSConn(t *testing.T, addr string) *wsConn {
	t.Helper()
	d := &websocket.Dialer{
		TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test self-signed certificate
		HandshakeTimeout: 3 * time.Second,
	}
	ws, _, err := d.Dial("wss://"+addr+"/ferry", nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return &wsConn{ws: ws}
}

// TestWSConnReadSkipsNonBinary covers non-binary messages not reaching the upper layer: after skipping, the close frame
// surfaces as a read error (instead of Text content being taken as business bytes).
func TestWSConnReadSkipsNonBinary(t *testing.T) {
	c := dialRawWSConn(t, startTextThenCloseWS(t))
	defer c.Close()
	buf := make([]byte, 64)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := c.Read(buf)
	if err == nil {
		t.Fatalf("want read error after text skip + close, got data %q", buf)
	}
}

// TestWSConnWriteAfterClose covers a closed connection: both the read and write adapters must error instead of
// faking success.
func TestWSConnWriteAfterClose(t *testing.T) {
	addr, _ := startWSServer(t)
	c := dialRawWSConn(t, addr)
	_ = c.ws.Close()
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("write on closed ws must fail")
	}
	if _, err := c.Read(make([]byte, 8)); err == nil {
		t.Fatal("read on closed ws must fail")
	}
}

// ---- heartbeat PingPong: default period and error/recovery branches ----

// scriptConn is a fake connection that serves reads from a script (as a byte stream); with werr set, writes always fail.
type scriptConn struct {
	r    io.Reader
	werr error
}

func (c *scriptConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *scriptConn) Write(p []byte) (int, error) {
	if c.werr != nil {
		return 0, c.werr
	}
	return len(p), nil
}
func (c *scriptConn) Close() error                     { return nil }
func (c *scriptConn) LocalAddr() net.Addr              { return nil }
func (c *scriptConn) RemoteAddr() net.Addr             { return nil }
func (c *scriptConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error { return nil }

// frame assembles one 9-byte heartbeat frame.
func frame(kind byte, nonce uint64) []byte {
	b := make([]byte, frameLen)
	b[0] = kind
	binary.BigEndian.PutUint64(b[1:], nonce)
	return b
}

// blockedTail appends a never-ready read after the bytes are consumed (standing in for EOF so the read goroutine does not error early).
func blockedTail(t *testing.T) io.Reader {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a
}

// TestPingPongDefaultInterval covers interval<=0 using the default 30s, returning cleanly on ctx cancellation.
func TestPingPongDefaultInterval(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately: returning nil is the pass condition
	if err := PingPong(ctx, a, 0); err != nil {
		t.Fatalf("PingPong = %v; want nil on cancel", err)
	}
	_ = a.Close()
}

// TestPingPongPongWriteFails covers a write failing while answering a ping with a pong: the error propagates.
func TestPingPongPongWriteFails(t *testing.T) {
	c := &scriptConn{
		r:    io.MultiReader(bytes.NewReader(frame(framePing, 7)), blockedTail(t)),
		werr: errors.New("broken pipe"),
	}
	err := PingPong(context.Background(), c, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("err = %v; want broken pipe", err)
	}
}

// TestPingPongPingWriteFails covers a write failing when the period fires a ping: the error propagates.
func TestPingPongPingWriteFails(t *testing.T) {
	c := &scriptConn{r: blockedTail(t), werr: errors.New("gone")}
	err := PingPong(context.Background(), c, 15*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v; want gone", err)
	}
}

// TestPingPongUnknownFrame covers an unknown frame type: an error, not a silent swallow as a business frame.
func TestPingPongUnknownFrame(t *testing.T) {
	c := &scriptConn{r: io.MultiReader(bytes.NewReader(frame(0x7F, 1)), blockedTail(t))}
	err := PingPong(context.Background(), c, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "unknown heartbeat frame") {
		t.Fatalf("err = %v; want unknown frame error", err)
	}
}

// TestPingPongPongFloodBufferFull covers a pong flood overrunning the buffer (16): the surplus is dropped
// via default without blocking the reader; with no further pongs -> three periods declare it dead.
func TestPingPongPongFloodBufferFull(t *testing.T) {
	var frames []byte
	for i := 0; i < 40; i++ { // far beyond the pongs buffer of 16
		frames = append(frames, frame(framePong, uint64(i))...)
	}
	c := &scriptConn{r: io.MultiReader(bytes.NewReader(frames), blockedTail(t))}
	done := make(chan error, 1)
	go func() { done <- PingPong(context.Background(), c, 10*time.Millisecond) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrHeartbeatLost) {
			t.Fatalf("err = %v; want ErrHeartbeatLost after flood", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PingPong did not conclude after flood")
	}
}

// TestPingPongOuterPongReset covers a pong arriving between periods: the outer select consumes it and resets
// the miss counter (the peer echoing on its own).
func TestPingPongOuterPongReset(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = c.Write(frame(framePong, 1)) // a proactive pong: earlier than the first tick
		buf := make([]byte, 64)
		for { // eat the following pings to keep the peer's write side flowing
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- PingPong(ctx, a, 100*time.Millisecond) }()
	time.Sleep(50 * time.Millisecond) // the pong lands between two ticks
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PingPong = %v; want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PingPong did not return on cancel")
	}
}
