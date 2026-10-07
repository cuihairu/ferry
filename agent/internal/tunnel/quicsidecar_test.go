package tunnel

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// startFakeSidecar 起一段本地桥替身：读 CONNECT 行按脚本应答，OK 后做 echo。
func startFakeSidecar(t *testing.T, reply func(req string) string) string {
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
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				resp := reply(line)
				if _, err := io.WriteString(c, resp); err != nil {
					return
				}
				if strings.HasPrefix(resp, "OK") {
					_, _ = io.Copy(c, c) // echo 作传输替身
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// TestQUICSidecarDial 覆盖 quic 插件：OK 放行（后续字节直通）、ERR 带原因、
// 异常应答拒绝、sidecar 不在快速失败。
func TestQUICSidecarDial(t *testing.T) {
	okAddr := startFakeSidecar(t, func(req string) string {
		if !strings.HasPrefix(req, "CONNECT landing.example.com:443 sni=camo.example.com") {
			return "ERR unexpected request\n"
		}
		return "OK\n"
	})
	errAddr := startFakeSidecar(t, func(string) string { return "ERR landing down\n" })
	junkAddr := startFakeSidecar(t, func(string) string { return "220 welcome\r\n" })

	t.Run("ok passthrough", func(t *testing.T) {
		t.Setenv(SidecarEnv, okAddr)
		tn, err := Open(pluginQUIC, Options{Timeout: 3 * time.Second, ServerName: "camo.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := tn.Dial(t.Context(), "landing.example.com:443")
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		got := make([]byte, 5)
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("echo: %v", err)
		}
		if string(got) != "hello" {
			t.Fatalf("echo = %q", got)
		}
	})

	t.Run("err reply", func(t *testing.T) {
		t.Setenv(SidecarEnv, errAddr)
		tn, err := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tn.Dial(t.Context(), "landing.example.com:443")
		if err == nil || !strings.Contains(err.Error(), "landing down") {
			t.Fatalf("want ERR reason in error, got %v", err)
		}
	})

	t.Run("unexpected reply", func(t *testing.T) {
		t.Setenv(SidecarEnv, junkAddr)
		tn, _ := Open(pluginQUIC, Options{Timeout: 3 * time.Second})
		_, err := tn.Dial(t.Context(), "landing.example.com:443")
		if err == nil || !strings.Contains(err.Error(), "unexpected sidecar reply") {
			t.Fatalf("want unexpected-reply error, got %v", err)
		}
	})

	t.Run("sidecar down", func(t *testing.T) {
		t.Setenv(SidecarEnv, "127.0.0.1:1")
		tn, _ := Open(pluginQUIC, Options{Timeout: 2 * time.Second})
		_, err := tn.Dial(t.Context(), "landing.example.com:443")
		if err == nil || !strings.Contains(err.Error(), "sidecar") {
			t.Fatalf("want sidecar dial error, got %v", err)
		}
	})
}
