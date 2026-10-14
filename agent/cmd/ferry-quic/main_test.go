package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 下面只覆盖起监听之前的参数校验路径：真正 listen/serve 会占端口，不进测试。

func TestRunClientMissingCAFile(t *testing.T) {
	err := runClient(context.Background(), []string{"--ca", filepath.Join(t.TempDir(), "nope.pem")})
	if err == nil || !strings.Contains(err.Error(), "read ca") {
		t.Fatalf("err = %v; want read ca failure", err)
	}
}

func TestRunClientBadCAPEM(t *testing.T) {
	// 文件存在但不是 PEM：AppendCertsFromPEM 应拒。
	bad := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write bad pem: %v", err)
	}
	err := runClient(context.Background(), []string{"--ca", bad})
	if err == nil || !strings.Contains(err.Error(), "no certificates in") {
		t.Fatalf("err = %v; want no certificates error", err)
	}
}

func TestRunClientBadListen(t *testing.T) {
	// 合法 CA 省略 → 直接进 net.Listen，非法地址应在监听处失败。
	err := runClient(context.Background(), []string{"--listen", "256.256.256.256:99999"})
	if err == nil {
		t.Fatal("invalid listen address should error")
	}
}

func TestRunServerRequiresCertKey(t *testing.T) {
	cert := filepath.Join(t.TempDir(), "c.pem")
	key := filepath.Join(t.TempDir(), "k.pem")

	if err := runServer(context.Background(), []string{}); err == nil ||
		!strings.Contains(err.Error(), "必填") {
		t.Fatalf("no cert/key err = %v; want required error", err)
	}
	if err := runServer(context.Background(), []string{"--cert", cert}); err == nil ||
		!strings.Contains(err.Error(), "必填") {
		t.Fatalf("cert only err = %v; want required error", err)
	}
	if err := runServer(context.Background(), []string{"--key", key}); err == nil ||
		!strings.Contains(err.Error(), "必填") {
		t.Fatalf("key only err = %v; want required error", err)
	}
}

func TestRunServerBadCertPair(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "c.pem")
	key := filepath.Join(dir, "k.pem")
	if err := os.WriteFile(cert, []byte("junk"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(key, []byte("junk"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	err := runServer(context.Background(), []string{"--cert", cert, "--key", key})
	if err == nil || !strings.Contains(err.Error(), "load cert") {
		t.Fatalf("err = %v; want load cert failure", err)
	}
}

// TestRunDispatch 模式分发口径：缺模式/未知模式回用法错误（ErrUsage，
// main 据此退出 2）；client 模式正常进入启动链路（ctx 已取消即回收）。
func TestRunDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{nil, {"bogus"}} {
		if err := run(context.Background(), args); !errors.Is(err, ErrUsage) {
			t.Fatalf("run(%v) = %v; want ErrUsage", args, err)
		}
	}
	if err := run(ctx, []string{"client", "--listen", "127.0.0.1:0", "--insecure"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("run(client) = %v; want canceled", err)
	}
}

// TestRunClientServesUntilCancel 覆盖起监听后的主路径：本地桥监听成功，
// ctx 取消即回收（Serve 按 ctx 口径返回 context.Canceled）。
func TestRunClientServesUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	if err := runClient(ctx, []string{"--listen", "127.0.0.1:0", "--insecure"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("runClient = %v; want canceled", err)
	}
}

// TestRunServerServesUntilCancel 覆盖 server 主路径：QUIC（UDP）监听成功，
// ctx 取消即回收。证书为自签临时对（测试不依赖任何外部资产）。
func TestRunServerServesUntilCancel(t *testing.T) {
	certPEM, keyPEM := selfSignedPEM(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := runServer(ctx, []string{"--listen", "127.0.0.1:0", "--target", "127.0.0.1:1",
		"--cert", certPEM, "--key", keyPEM})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runServer = %v; want canceled", err)
	}
}

// selfSignedPEM 生成自签 ECDSA 证书对（server 模式必填 --cert/--key）。
func selfSignedPEM(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ferry-quic-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert := filepath.Join(dir, "c.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "k.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, keyPath
}
