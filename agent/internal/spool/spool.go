// Package spool is the local IPC between the core and the role components (E-5/E-7):
//
// role processes (-role probe etc.) do not connect to the panel directly -- the single connection per node belongs to the core,
// and a second one would kick the core offline. A role writes its report Envelopes line by line into
// <spool_dir>/<role>.out, which the core's Watch forwards. A role that crashes only loses its own file;
// the heartbeat is unaffected.
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

// PollInterval is the Watch scan period.
const PollInterval = 2 * time.Second

// pathFor returns a role's report file.
func pathFor(dir, role string) string {
	return filepath.Join(dir, role+".out")
}

// Write appends one report (an atomic O_APPEND write, consumed line by line by the core).
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

// Watch scans the spool directory periodically and forwards (blocking until the ctx is cancelled).
func Watch(ctx context.Context, dir string, send func(agentproto.Envelope) error) error {
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		// a failed forward (offline etc.) loses no data: the offset does not advance and the next round resends.
		_ = w.drain(send)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watcher remembers each file's consumption offset; a smaller file counts as rotated and is read from the start.
type watcher struct {
	dir     string
	offsets map[string]int64
}

// drain consumes every role file. A send error stops it (the offset does not advance, so the next round resends).
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
		off = 0 // rotated/recreated
	}
	if _, err := f.Seek(off, 0); err != nil {
		return err
	}
	// consume only up to the last '\n': a trailing partial line is a tear from a concurrent append, read again next round.
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
		return nil // all partial lines, wait for the next round
	}
	newOff := off + int64(cut) + 1
	for _, line := range bytes.Split(rest[:cut], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// skip bad lines (the offset still advances, otherwise it would stall forever).
			continue
		}
		if err := send(env); err != nil {
			return err
		}
	}
	w.offsets[path] = newOff
	return nil
}
