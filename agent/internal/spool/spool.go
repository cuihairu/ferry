// Package spool 是核心与角色组件之间的本机 IPC（E-5/E-7）：
//
// 角色进程（-role probe 等）不直连面板——同节点单连接归核心所有，
// 第二条连接会把核心踢下线。角色把上报 Envelope 按行写进
// <spool_dir>/<role>.out，核心 Watch 转发。崩溃的角色只丢自己的文件，
// 心跳不受影响。
package spool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// PollInterval 是 Watch 的扫描周期。
const PollInterval = 2 * time.Second

// pathFor 返回角色的上报文件。
func pathFor(dir, role string) string {
	return filepath.Join(dir, role+".out")
}

// Write 追加一条上报（原子 O_APPEND 写，核心按行消费）。
func Write(dir, role string, env agentproto.Envelope) error {
	if dir == "" || role == "" {
		return fmt.Errorf("spool: dir and role are required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("spool: mkdir: %w", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("spool: marshal: %w", err)
	}
	f, err := os.OpenFile(pathFor(dir, role), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("spool: open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("spool: write: %w", err)
	}
	return nil
}

// Watch 周期扫描 spool 目录并转发（阻塞到 ctx 取消）。
func Watch(ctx context.Context, dir string, send func(agentproto.Envelope) error) error {
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		// 转发失败（离线等）不丢数据：offset 不推进，下轮重发。
		_ = w.drain(send)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watcher 记住每个文件的消费偏移；文件变小视为轮转过，从头读。
type watcher struct {
	dir     string
	offsets map[string]int64
}

// drain 消费全部角色文件。send 报错即停（offset 不推进，下轮重发）。
func (w *watcher) drain(send func(agentproto.Envelope) error) error {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".out") {
			continue
		}
		if err := w.drainFile(filepath.Join(w.dir, e.Name()), send); err != nil {
			return err
		}
	}
	return nil
}

func (w *watcher) drainFile(path string, send func(agentproto.Envelope) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	off := w.offsets[path]
	if st.Size() < off {
		off = 0 // 轮转/重建过
	}
	if _, err := f.Seek(off, 0); err != nil {
		return err
	}
	// 只消费到最后一个 '\n' 为止：尾部半行是并发追加中的撕裂，下轮再读。
	rest := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, err := f.Read(tmp)
		rest = append(rest, tmp[:n]...)
		if err != nil {
			break
		}
	}
	cut := bytes.LastIndexByte(rest, '\n')
	if cut < 0 {
		return nil // 全是半行，等下轮
	}
	newOff := off + int64(cut) + 1
	for _, line := range bytes.Split(rest[:cut], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// 坏行跳过（offset 照推进，否则永远卡住）。
			continue
		}
		if err := send(env); err != nil {
			return err
		}
	}
	w.offsets[path] = newOff
	return nil
}
