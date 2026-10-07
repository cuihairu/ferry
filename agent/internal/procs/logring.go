package procs

import (
	"strings"
	"sync"
)

// logRing 是单进程运行日志的内存环形缓冲（P1-11）：进程 stdout/stderr
// 镜像进固定容量环形缓冲供面板拉取，不落盘、agent 重启即空。
type logRing struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

const procLogCap = 500

func newLogRing(capacity int) *logRing {
	if capacity < 1 {
		capacity = 1
	}
	return &logRing{lines: make([]string, capacity)}
}

// Write 实现 io.Writer，适配 exec.Cmd 的输出（管道复制可能把多行并成
// 一次写入，按内嵌换行拆行；跳过空行）。
func (r *logRing) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			r.add(line)
		}
	}
	return len(p), nil
}

func (r *logRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.next] = line
	r.next++
	if r.next >= len(r.lines) {
		r.next = 0
		r.full = true
	}
}

// snapshot 返回最近的至多 limit 行（时间升序，最新在后）。
func (r *logRing) snapshot(limit int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.next
	if r.full {
		n = len(r.lines)
	}
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]string, n)
	start := r.next - n
	if start < 0 {
		start += len(r.lines)
	}
	for i := 0; i < n; i++ {
		out[i] = r.lines[(start+i)%len(r.lines)]
	}
	return out
}
