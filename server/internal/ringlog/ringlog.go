// Package ringlog 面板运行日志的内存环形缓冲（P1-11，对齐 3x-ui 日志查看）：
// 把 std log 与 gin 访问日志镜像进固定容量环形缓冲，供管理端查看与清除，
// 不落盘、重启即空。
package ringlog

import (
	"strings"
	"sync"
	"time"
)

// Entry 是一条运行日志。
type Entry struct {
	Time time.Time `json:"time"`
	Line string    `json:"line"`
}

// Log 是固定容量的环形缓冲，并发安全；容量满后覆盖最旧条目。
type Log struct {
	mu   sync.Mutex
	ring []Entry
	next int
	full bool
}

// defaultLog 是进程级共享实例：main 把它接进 std log/gin 输出，
// 管理端接口直接读取，避免改路由装配签名。
var defaultLog = New(1000)

// Default 返回进程级共享环形缓冲。
func Default() *Log { return defaultLog }

// New 创建容量 capacity 的环形缓冲。
func New(capacity int) *Log {
	if capacity < 1 {
		capacity = 1
	}
	return &Log{ring: make([]Entry, capacity)}
}

// Add 追加一条日志（时间取当前）。
func (l *Log) Add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = Entry{Time: time.Now(), Line: line}
	l.next++
	if l.next >= len(l.ring) {
		l.next = 0
		l.full = true
	}
}

// Write 实现 io.Writer，适配 log.Logger/gin 的整行输出（去除尾部换行）。
func (l *Log) Write(p []byte) (int, error) {
	l.Add(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Entries 返回最近的至多 limit 条（时间升序，最新在后）；limit<=0 表示全部。
func (l *Log) Entries(limit int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.ring)
	if l.full {
		n = len(l.ring)
	} else {
		n = l.next
	}
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]Entry, n)
	start := l.next - n
	if start < 0 {
		start += len(l.ring)
	}
	for i := 0; i < n; i++ {
		out[i] = l.ring[(start+i)%len(l.ring)]
	}
	return out
}

// Clear 清空缓冲。
func (l *Log) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.ring {
		l.ring[i] = Entry{}
	}
	l.next = 0
	l.full = false
}
