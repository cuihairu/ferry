package quictunnel

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// selfSignedTLSPair 生成内存自签证书（IP SAN 127.0.0.1/localhost）与其信任池，
// 覆盖「服务端证书 + 客户端校验」全链，不用 insecure 快捷路径。
func selfSignedTLSPair(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ferry-quic-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(mustLeaf(t, der))
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func mustLeaf(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

// startEchoTarget 起一段 TCP echo（落地服务替身），返回其地址。
func startEchoTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// TestEndToEnd 全链路：本地桥 → CONNECT → QUIC 流 → server → echo target。
// 双向各跑一份数据，模拟 relay 的双向拷贝。
func TestEndToEnd(t *testing.T) {
	cert, pool := selfSignedTLSPair(t)
	target := startEchoTarget(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// server：QUIC 监听随机 UDP 端口
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
	go func() { _ = ServeServer(ctx, qln, target, log.Default()) }()
	serverAddr := udp.LocalAddr().String()

	// client：本地桥监听随机端口
	client := NewClient(&tls.Config{RootCAs: pool})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = client.Serve(ctx, ln, log.Default()) }()

	// 模拟进程内 quic 插件：拨本地桥、发 CONNECT、收 OK 后双向拷贝
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sni := "127.0.0.1"
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("CONNECT " + serverAddr + " sni=" + sni + "\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if reply != "OK\n" {
		t.Fatalf("unexpected reply %q", reply)
	}

	payload := []byte("ping-through-quic- transporting-raw-quic-not-hysteria2")
	go func() {
		for i := 0; i < 3; i++ {
			if _, err := conn.Write(payload); err != nil {
				return
			}
		}
	}()
	got := make([]byte, 3*len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	for i := 0; i < 3; i++ {
		if string(got[i*len(payload):(i+1)*len(payload)]) != string(payload) {
			t.Fatalf("echo mismatch at round %d", i)
		}
	}
}

// TestServeClientErrReply 目标不可达时本地桥应回 ERR 而不是悬挂。
func TestServeClientErrReply(t *testing.T) {
	_, pool := selfSignedTLSPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// server 起了就关：端口在但无监听 → QUIC 拨号失败
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverAddr := udp.LocalAddr().String()
	_ = udp.Close()

	client := NewClient(&tls.Config{RootCAs: pool})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = client.Serve(ctx, ln, log.Default()) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// 客户端侧带超时：ERR 必须在限期内回来（拨号侧 10s 门限 + QUIC 握手
	// 默认 5s idle 超时先触发，ERR 应在 15s 内落到本地）
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write([]byte("CONNECT " + serverAddr + " sni=127.0.0.1\n")); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read err reply: %v", err)
	}
	if len(reply) < 3 || string(reply[:3]) != "ERR" {
		t.Fatalf("want ERR reply, got %q", reply)
	}
}

// TestParseConnectLine 覆盖请求行解析：前缀/缺地址/sni 覆盖/缺省取 host。
func TestParseConnectLine(t *testing.T) {
	if _, _, err := ParseConnectLine("GET / HTTP/1.1\r\n"); err == nil {
		t.Fatal("non-CONNECT line should fail")
	}
	if _, _, err := ParseConnectLine("CONNECT\n"); err == nil {
		t.Fatal("missing addr should fail")
	}
	addr, sni, err := ParseConnectLine("CONNECT landing.example.com:443 sni=camo.example.com\n")
	if err != nil || addr != "landing.example.com:443" || sni != "camo.example.com" {
		t.Fatalf("got %q/%q err=%v", addr, sni, err)
	}
	addr, sni, err = ParseConnectLine("CONNECT 10.0.0.1:8000\n")
	if err != nil || addr != "10.0.0.1:8000" || sni != "10.0.0.1" {
		t.Fatalf("default sni from host: got %q/%q err=%v", addr, sni, err)
	}
}
