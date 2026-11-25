package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAgentWSProcStatus covers heartbeat process snapshot persistence (SAVE-3):
// the heartbeat carries ProcStatus (with metrics) -> upserted into
// node_proc_statuses -> repeated heartbeats refresh without duplicating rows ->
// GET /api/nodes/:id/procs reads it back (metrics column parsed back to a numeric object).
func TestAgentWSProcStatus(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "pstat-1")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "pstat-1", token)

	sendHB := func(procs []agentproto.ProcStatus) {
		t.Helper()
		env, _ := agentproto.NewEnvelope(fmt.Sprintf("hb-%d", time.Now().UnixNano()),
			agentproto.MsgHeartbeat, agentproto.Heartbeat{Procs: procs, At: time.Now()})
		if err := c.WriteJSON(env); err != nil {
			t.Fatalf("write heartbeat: %v", err)
		}
		if got := readEnv(t, c); got.Type != agentproto.MsgHeartbeatAck {
			t.Fatalf("expected heartbeat_ack, got %s", got.Type)
		}
	}

	// First heartbeat: the cache process carries hit-counter metrics
	sendHB([]agentproto.ProcStatus{{
		Name: "cache", State: "running", PID: 4321, Restarts: 1, Since: time.Now(),
		Metrics: map[string]uint64{"hits": 12, "misses": 3},
	}})
	var rows []storage.NodeProcStatus
	if err := db.Order("proc ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query proc statuses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("proc statuses = %d, want 1", len(rows))
	}
	var got map[string]uint64
	if err := json.Unmarshal([]byte(rows[0].Metrics), &got); err != nil {
		t.Fatalf("metrics column not json: %q", rows[0].Metrics)
	}
	if rows[0].Proc != "cache" || rows[0].State != "running" || rows[0].PID != 4321 ||
		rows[0].Restarts != 1 || got["hits"] != 12 || got["misses"] != 3 {
		t.Fatalf("proc row mismatch: %+v metrics=%v", rows[0], got)
	}

	// Second heartbeat: refreshing the same process does not duplicate the row (metrics updated), a new process gets its own row
	sendHB([]agentproto.ProcStatus{
		{Name: "cache", State: "running", PID: 4321, Restarts: 1, Since: time.Now(),
			Metrics: map[string]uint64{"hits": 99, "misses": 1}},
		{Name: "squid", State: "stopped", Since: time.Now()},
	})
	if err := db.Order("proc ASC").Find(&rows).Error; err != nil {
		t.Fatalf("requery proc statuses: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("proc statuses = %d, want 2 (refresh must not duplicate)", len(rows))
	}
	if rows[0].Proc != "cache" || rows[0].Restarts != 1 {
		t.Fatalf("cache row mismatch: %+v", rows[0])
	}
	_ = json.Unmarshal([]byte(rows[0].Metrics), &got)
	if got["hits"] != 99 || got["misses"] != 1 {
		t.Fatalf("cache metrics not refreshed: %v", got)
	}

	// GET read-back: metrics parsed back to a numeric object, the field omitted for processes without metrics
	rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/procs", id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get node procs: %d %s", rec.Code, rec.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode node procs: %v", err)
	}
	if len(list) != 2 || list[0]["proc"] != "cache" || list[1]["proc"] != "squid" {
		t.Fatalf("node procs list mismatch: %v", list)
	}
	m, ok := list[0]["metrics"].(map[string]any)
	if !ok || m["hits"] != float64(99) {
		t.Fatalf("cache metrics in api: %v", list[0]["metrics"])
	}
	if _, has := list[1]["metrics"]; has {
		t.Fatalf("metrics must be omitted without data: %v", list[1])
	}
	if v, has := list[0]["pid"]; !has || v != float64(4321) {
		t.Fatalf("cache pid in api: %v", list[0]["pid"])
	}

	// Nonexistent node -> 404
	if rec := doJSON(t, r, "GET", "/api/nodes/9999/procs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing node should 404: %d", rec.Code)
	}
}
