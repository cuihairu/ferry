package spool

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestWriteOpenFailsWithSlashRole 带路径分隔符的 role：拼出的目标路径父目录
// 不存在，open 失败要报错（而不是静默成功）。
func TestWriteOpenFailsWithSlashRole(t *testing.T) {
	dir := t.TempDir()
	err := Write(dir, "sub/role", mustEnv(t, "1", agentproto.MsgProbeReport))
	if err == nil || !strings.Contains(err.Error(), "spool: open") {
		t.Fatalf("err = %v; want spool: open", err)
	}
}

// TestDrainRotationResetsOffset 文件被轮转/重建（变小）：偏移复位从头消费，
// 新内容不因旧偏移越过而被丢。
func TestDrainRotationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	path := pathFor(dir, "probe")

	var got []string
	forward := func(e agentproto.Envelope) error {
		got = append(got, e.ID)
		return nil
	}
	w := &watcher{dir: dir, offsets: map[string]int64{}}

	if err := Write(dir, "probe", mustEnv(t, "long-content-1", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	if err := w.drain(forward); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "long-content-1" {
		t.Fatalf("first drain = %v", got)
	}

	// 轮转：整体重写成更短的新内容
	fresh := mustEnv(t, "s", agentproto.MsgProbeReport)
	raw, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := w.drain(forward); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "s" {
		t.Fatalf("after rotation = %v; want fresh content", got)
	}
}

// TestDrainSkipsEmptyLines 空行（含连续空行）跳过且不误发。
func TestDrainSkipsEmptyLines(t *testing.T) {
	dir := t.TempDir()
	env := mustEnv(t, "e1", agentproto.MsgProbeReport)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw) + "\n\n\n" + string(raw) + "\n"
	if err := os.WriteFile(pathFor(dir, "probe"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var got []string
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("forwarded = %v; want exactly 2 (empty lines skipped)", got)
	}
}
