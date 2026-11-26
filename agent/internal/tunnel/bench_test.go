package tunnel

// F-0829: comparative throughput benchmarks for the transport plugins. Every
// plugin bridges one connection to the same loopback echo; each op pushes a
// 1 MiB payload through and reads it back, and b.SetBytes makes `go test
// -bench BenchmarkPlugin -benchmem` report MiB/s so the numbers compare.
//
// The quic plugin is not benchmarked here: its sidecar dial targets a fixed
// localhost port managed outside this process (the ferry-quic binary), so an
// in-package benchmark cannot stand one up honestly -- the ferry-quic module
// carries its own end-to-end tests.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const benchPayload = 1 << 20 // 1 MiB per op

func benchRoundtrip(b *testing.B, tn Tunnel, addr string) {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := tn.Dial(ctx, addr)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	payload := make([]byte, benchPayload)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	back := make([]byte, benchPayload)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		if _, err := io.ReadFull(conn, back); err != nil {
			b.Fatalf("read: %v", err)
		}
	}
}

// benchEcho listens on loopback and copies every connection both ways.
func benchEcho(b *testing.B) net.Listener {
	b.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	b.Cleanup(func() { _ = ln.Close() })
	return ln
}

func writeCACert(b *testing.B, der []byte) string {
	b.Helper()
	caPath := filepath.Join(b.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		b.Fatal(err)
	}
	return caPath
}

// BenchmarkPluginTLSCamo is the reference point: TLS with browser-aligned
// ClientHello straight to the landing.
func BenchmarkPluginTLSCamo(b *testing.B) {
	cert := testCert(b)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil },
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	tn, err := Open(DefaultPlugin, Options{ServerName: "cover.example.com", CAFile: writeCACert(b, cert.Certificate[0])})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tn.Close() })
	benchRoundtrip(b, tn, ln.Addr().String())
}

// BenchmarkPluginWSTLS wraps the same echo in a wss websocket.
func BenchmarkPluginWSTLS(b *testing.B) {
	addr, caFile := startWSServer(b)
	tn, err := Open("ws-tls", Options{ServerName: "cover.example.com", CAFile: caFile})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = tn.Close() })
	benchRoundtrip(b, tn, addr)
}

// BenchmarkPluginSSH bridges through the in-memory sshd's direct-tcpip
// channels (x/crypto loopback, no real sshd).
func BenchmarkPluginSSH(b *testing.B) {
	echo := benchEcho(b)
	srv := startMemSSH(b, echo.Addr().String())
	tn := newSSHTunnel(b, srv.sshdAddr(), echo.Addr().String(), srv.hostKeyLine)
	benchRoundtrip(b, tn, echo.Addr().String())
}
