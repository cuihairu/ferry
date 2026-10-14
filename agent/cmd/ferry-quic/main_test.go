package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
