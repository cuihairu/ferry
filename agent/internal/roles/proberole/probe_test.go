package proberole

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

func TestRoleExitsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, config.Config{}, log.New(io.Discard, "", 0))
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("role did not exit on cancel")
	}
}

// dialTarget 起一个只接受连接的本地 listener，返回 host:port。
func dialTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

// runBrief 起 Run 并等它探测一轮后取消（5 轮 × 200ms 间隔 ≈ 0.8s）。
func runBrief(t *testing.T, cfg config.Config) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, log.New(io.Discard, "", 0)) }()
	time.Sleep(1500 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v; want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("role did not exit on cancel")
	}
}

// TestRoleForwardsToSpool 角色进程只经 spool 上报（不直连面板，E-7）：
// 探测结论落成 spool 行文件，载荷可解码回 ProbeReport。
func TestRoleForwardsToSpool(t *testing.T) {
	spoolDir := filepath.Join(t.TempDir(), "spool")
	cfg := config.Config{
		SpoolDir: spoolDir,
		Probes: []config.ProbeSpec{{
			Name: "core-api", TargetKind: "tunnel", Target: dialTarget(t),
			Direction: "out", IntervalSec: 3600,
		}},
	}
	runBrief(t, cfg)

	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		t.Fatalf("read spool dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no probe report spooled")
	}
	raw, err := os.ReadFile(filepath.Join(spoolDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read spool file: %v", err)
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode spooled envelope: %v", err)
	}
	if env.Type != agentproto.MsgProbeReport {
		t.Fatalf("type = %q; want probe report", env.Type)
	}
	var batch agentproto.ProbeReportBatch
	if err := env.Decode(&batch); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(batch.Items) != 1 || !batch.Items[0].Reachable {
		t.Fatalf("items = %+v; want one reachable report", batch.Items)
	}
}

// TestRoleNoSpoolDirDrops 无 spool_dir 是调试形态：探测照跑，上报进黑洞。
func TestRoleNoSpoolDirDrops(t *testing.T) {
	cfg := config.Config{
		Probes: []config.ProbeSpec{{
			Name: "core-api", TargetKind: "tunnel", Target: dialTarget(t),
			IntervalSec: 3600,
		}},
	}
	runBrief(t, cfg) // 不 panic、不落盘即可
}
