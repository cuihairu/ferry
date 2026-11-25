//go:build unix

package procs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// metricsResp is a switchable response of the test metrics endpoint (status code + body).
type metricsResp struct {
	code int
	body string
}

// TestMetricsParser covers metric parsing: a JSON numeric object, text key-value
// lines (empty lines/word-only lines/non-numeric values skipped), and empty or garbage input yielding an empty map.
func TestMetricsParser(t *testing.T) {
	p := newMetricsParser()

	// a JSON numeric object
	m, err := p.Parse(strings.NewReader(`{"hits":123,"misses":45}`))
	if err != nil || m["hits"] != 123 || m["misses"] != 45 {
		t.Fatalf("json parse = %v, %v", m, err)
	}

	// text key-value lines: empty and word-only lines skipped, non-numeric values skipped
	m, err = p.Parse(strings.NewReader("hits 123\n\nbadline\nmisses notanum\nproxies 7\n"))
	if err != nil || m["hits"] != 123 || m["proxies"] != 7 {
		t.Fatalf("text parse = %v, %v", m, err)
	}
	if _, ok := m["misses"]; ok {
		t.Fatalf("non-numeric value must be skipped: %v", m)
	}

	// empty and garbage input: an empty map without an error
	for _, in := range []string{"", "   \n", "not json at all"} {
		m, err = p.Parse(strings.NewReader(in))
		if err != nil || len(m) != 0 {
			t.Fatalf("parse %q = %v, %v", in, m, err)
		}
	}
}

// TestCollectProcMetrics covers the metric collection goroutine: MetricsURL polling results
// are attached to the status snapshot and reported with the heartbeat (both the text-line and JSON paths); when the endpoint fails
// the snapshot is cleared without leaving stale values.
func TestCollectProcMetrics(t *testing.T) {
	var cur atomic.Value
	cur.Store(metricsResp{code: http.StatusOK, body: "hits 12\nmisses 3\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r := cur.Load().(metricsResp)
		if r.code != http.StatusOK {
			w.WriteHeader(r.code)
			return
		}
		fmt.Fprint(w, r.body)
	}))
	defer srv.Close()

	m, _ := testManager(t, []config.ProcSpec{{
		Name:       "cache",
		Kind:       "nginx",
		Exec:       "sh",
		Args:       []string{"-c", "sleep 60"},
		MetricsURL: srv.URL,
	}})
	m.metricsInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "cache", agentproto.ProcRunning)

	// text-line metrics reported with the status snapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics != nil &&
				s.Metrics["hits"] == 12 && s.Metrics["misses"] == 3 {
				goto collected
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("text metrics never collected")
collected:

	// a JSON object is collectable the same way
	cur.Store(metricsResp{code: http.StatusOK, body: `{"hits": 99, "misses": 1}`})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics != nil && s.Metrics["hits"] == 99 {
				goto jsonCollected
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("json metrics never collected")
jsonCollected:

	// endpoint 500: the snapshot is cleared, no stale values left
	cur.Store(metricsResp{code: http.StatusInternalServerError})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics == nil {
				goto cleared
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("metrics not cleared on endpoint failure")
cleared:

	// stop the process before finishing: wait for the goroutine's exit log to fall
	// inside the test window, so writing t.Output() after the test does not panic.
	if err := m.Control("cache", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "cache", agentproto.ProcStopped)
}

// TestCollectProcMetricsNoURL covers a process with no MetricsURL configured:
// no collection starts and the status snapshot has no metrics.
func TestCollectProcMetricsNoURL(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "plain", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	m.metricsInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "plain", agentproto.ProcRunning)
	time.Sleep(200 * time.Millisecond)
	for _, s := range m.Statuses() {
		if s.Name == "plain" && s.Metrics != nil {
			t.Fatalf("metrics must stay nil without MetricsURL: %v", s.Metrics)
		}
	}
	// stop the process before finishing: wait for the goroutine's exit log to fall
	// inside the test window, so writing t.Output() after the test does not panic.
	if err := m.Control("plain", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "plain", agentproto.ProcStopped)
}
