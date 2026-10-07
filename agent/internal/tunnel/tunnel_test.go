package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// testCert 现场签一张自签证书（测试专用，不落文件）。
func testCert(t *testing.T) tls.Certificate {
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
	if err := Register("dup", func(Options) (Tunnel, error) { return nil, nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := Register("dup", func(Options) (Tunnel, error) { return nil, nil }); err == nil {
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
		_, _ = c.Read(buf) // 完成握手
	}()

	// 自签 CA 落临时文件走 CAFile（私有部署同款路径）。
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
	// net.Pipe 是 rendezvous（无缓冲），双端同时写会互相卡死；
	// 心跳跑在 TCP 上，用回环 TCP 对测。
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
	_ = b.Close() // 对端直接消失
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("dead peer must return error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no error on dead peer")
	}
}
