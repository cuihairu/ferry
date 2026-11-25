// Package ringlog is the in-memory ring buffer of panel runtime logs (P1-11, aligned with 3x-ui log viewing):
// it mirrors std log and gin access logs into a fixed-capacity ring buffer for the admin side to view and clear,
// never writing to disk and starting empty again after a restart.
package ringlog

import (
	"strings"
	"sync"
	"time"
)

// Entry is one runtime log line.
type Entry struct {
	Time time.Time `json:"time"`
	Line string    `json:"line"`
}

// Log is a fixed-capacity ring buffer, concurrency-safe; once full it overwrites the oldest entries.
type Log struct {
	mu   sync.Mutex
	ring []Entry
	next int
	full bool
}

// defaultLog is the process-level shared instance: main wires it into std log/gin output,
// and the admin API reads it directly, avoiding changes to the route-assembly signature.
var defaultLog = New(1000)

// Default returns the process-level shared ring buffer.
func Default() *Log { return defaultLog }

// New creates a ring buffer with capacity capacity.
func New(capacity int) *Log {
	if capacity < 1 {
		capacity = 1
	}
	return &Log{ring: make([]Entry, capacity)}
}

// Add appends one log line (timed now).
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

// Write implements io.Writer, adapting to log.Logger/gin whole-line output (stripping the trailing newline).
func (l *Log) Write(p []byte) (int, error) {
	l.Add(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Entries returns at most the latest limit lines (ascending by time, newest last); limit<=0 means all.
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

// Clear empties the buffer.
func (l *Log) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.ring {
		l.ring[i] = Entry{}
	}
	l.next = 0
	l.full = false
}
