package procs

import (
	"strings"
	"sync"
)

// logRing is an in-memory ring buffer of one process's runtime log (P1-11): the process stdout/stderr
// is mirrored into a fixed-capacity ring buffer for the panel to fetch; nothing is written to disk and an agent restart empties it.
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

// Write implements io.Writer, adapting exec.Cmd output (a pipe copy can merge several lines into
// one write, so lines are split on embedded newlines; empty lines are skipped).
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

// snapshot returns at most the most recent limit lines (in ascending time order, newest last).
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
