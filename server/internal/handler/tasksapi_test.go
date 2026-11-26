package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/tasks"
)

// TestTaskEndpoints covers the unified task surface (F-1602/F-1607/F-1608/
// F-1611) and the pool-operation consumers (F-1628/F-1629) end to end: the
// test owns the manager so the sweep can be driven manually.
func TestTaskEndpoints(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	mgr := tasks.NewManager(db, nil)
	r, _ := NewRouterWithTasks(db, config.Default(), mgr)
	ctx := context.Background()

	// Unknown kinds never mint rows.
	if rec := doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d %s", rec.Code, rec.Body)
	}

	// Entry node for the suspend consumer.
	if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "n-suspend", "address": "n-suspend.example.com", "port": 443, "protocol": "vless", "role": "entry",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}

	// Enqueue the suspend task (F-1628); a repeated key returns the same row (F-1608).
	rec := doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "node.suspend", "ref_id": 1, "idempotency_key": "sup-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("enqueue: %d %s", rec.Code, rec.Body)
	}
	var task storage.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if task.Status != tasks.StatusPending {
		t.Fatalf("status = %s", task.Status)
	}
	rec = doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "node.suspend", "ref_id": 1, "idempotency_key": "sup-1"})
	var again storage.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &again)
	if again.ID != task.ID {
		t.Fatalf("idempotent enqueue minted %d, want %d", again.ID, task.ID)
	}

	// Cancel while pending (F-1607), then retry (F-1611) and let it run.
	if rec := doJSON(t, r, "POST", "/api/tasks/"+itoa64(int(task.ID))+"/cancel", nil); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/tasks/"+itoa64(int(task.ID)), nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	} else {
		var got storage.Task
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Status != tasks.StatusCancelled {
			t.Fatalf("cancelled status = %s", got.Status)
		}
	}
	rec = doJSON(t, r, "POST", "/api/tasks/"+itoa64(int(task.ID))+"/retry", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}

	// Retry of a pending task is a conflict.
	if rec := doJSON(t, r, "POST", "/api/tasks/"+itoa64(int(task.ID))+"/retry", nil); rec.Code != http.StatusConflict {
		t.Fatalf("retry pending: %d %s", rec.Code, rec.Body)
	}

	// Drive the sweep: the suspend task flips the node's pool state (F-1628).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mgr.LoopOnce(ctx)
		var n storage.Node
		if err := db.First(&n, 1).Error; err == nil && n.PoolState == "suspended" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var n storage.Node
	if err := db.First(&n, 1).Error; err != nil || n.PoolState != "suspended" {
		t.Fatalf("node pool state = %s err=%v", n.PoolState, err)
	}

	// Re-running the suspend consumer is a no-op success (already suspended).
	rec = doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "node.suspend", "ref_id": 1, "idempotency_key": "sup-2"})
	var t2 storage.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &t2)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mgr.LoopOnce(ctx)
		var got storage.Task
		_ = db.First(&got, t2.ID).Error
		if got.Status == tasks.StatusSucceeded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got2 storage.Task
	_ = db.First(&got2, t2.ID).Error
	if got2.Status != tasks.StatusSucceeded {
		t.Fatalf("re-suspend status = %s", got2.Status)
	}

	// Resume consumer (F-1629).
	rec = doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "node.resume", "ref_id": 1, "idempotency_key": "res-1"})
	var t3 storage.Task
	_ = json.Unmarshal(rec.Body.Bytes(), &t3)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mgr.LoopOnce(ctx)
		var n storage.Node
		if err := db.First(&n, 1).Error; err == nil && n.PoolState != "suspended" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var n3 storage.Node
	if err := db.First(&n3, 1).Error; err != nil || n3.PoolState == "suspended" {
		t.Fatalf("resume pool state = %s err=%v", n3.PoolState, err)
	}

	// List with filters (F-1602/F-1603 visibility).
	if rec := doJSON(t, r, "GET", "/api/tasks?kind=node.suspend&limit=10", nil); rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	} else {
		var rows []storage.Task
		_ = json.Unmarshal(rec.Body.Bytes(), &rows)
		if len(rows) != 2 {
			t.Fatalf("list rows = %d", len(rows))
		}
	}
}

// TestTaskAgentLinkConsumers covers the agent-link task kinds (F-1621 agent
// restart, F-1622 relay restart, F-1626 agent rollback, F-1627 node reprobe)
// end to end: a fake agent answers on the wire while the test drives the sweep.
func TestTaskAgentLinkConsumers(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	mgr := tasks.NewManager(db, nil)
	r, _ := NewRouterWithTasks(db, config.Default(), mgr)
	ctx := context.Background()

	id, token := createNodeFor(t, r, "tlink-1")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "tlink-1", token)

	// Serving loop: answer every kind the consumers send, with a mutable
	// switch that makes the next agent.restart refuse (OK=false).
	var failRestart atomic.Bool
	go func() {
		for {
			var env agentproto.Envelope
			if err := c.ReadJSON(&env); err != nil {
				return
			}
			var out agentproto.Envelope
			switch env.Type {
			case agentproto.MsgAgentRestart:
				ack := agentproto.AgentRestartAck{OK: !failRestart.Load()}
				if !ack.OK {
					ack.Error = "restart refused by test"
				}
				out, _ = agentproto.NewEnvelope(env.ID, agentproto.MsgAgentRestartAck, ack)
			case agentproto.MsgAgentRollback:
				out, _ = agentproto.NewEnvelope(env.ID, agentproto.MsgAgentRollbackAck, agentproto.AgentRollbackAck{OK: true})
			case agentproto.MsgProbeNow:
				out, _ = agentproto.NewEnvelope(env.ID, agentproto.MsgProbeNowAck, agentproto.ProbeNowAck{OK: true, Probed: 2})
			case agentproto.MsgProcCtl:
				var ctl agentproto.ProcCtl
				_ = env.Decode(&ctl)
				out, _ = agentproto.NewEnvelope(env.ID, agentproto.MsgProcCtlAck, agentproto.ProcCtlAck{Proc: ctl.Proc, Action: ctl.Action, OK: true})
			default:
				continue
			}
			if err := c.WriteJSON(out); err != nil {
				return
			}
		}
	}()

	// runTask enqueues one kind and drives the sweep until the row settles.
	runTask := func(kind string, ref uint, key string) storage.Task {
		t.Helper()
		rec := doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": kind, "ref_id": ref, "idempotency_key": key})
		if rec.Code != http.StatusCreated {
			t.Fatalf("enqueue %s: %d %s", kind, rec.Code, rec.Body)
		}
		var enq storage.Task
		_ = json.Unmarshal(rec.Body.Bytes(), &enq)
		deadline := time.Now().Add(12 * time.Second)
		var got storage.Task
		for time.Now().Before(deadline) {
			mgr.LoopOnce(ctx)
			_ = db.First(&got, enq.ID).Error
			switch got.Status {
			case tasks.StatusSucceeded, tasks.StatusFailed, tasks.StatusDead:
				return got
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("task %s key=%s did not settle (last: %s attempts=%d next=%v err=%q log=%q)", kind, key, got.Status, got.Attempts, got.NextAttemptAt, got.Error, got.LogLines)
		return storage.Task{}
	}

	if got := runTask("agent.restart", id, "agr-1"); got.Status != tasks.StatusSucceeded {
		t.Fatalf("agent.restart: %s (%s)", got.Status, got.Error)
	}
	if got := runTask("relay.restart", id, "rr-1"); got.Status != tasks.StatusSucceeded {
		t.Fatalf("relay.restart: %s (%s)", got.Status, got.Error)
	}
	if got := runTask("agent.rollback", id, "arb-1"); got.Status != tasks.StatusSucceeded {
		t.Fatalf("agent.rollback: %s (%s)", got.Status, got.Error)
	}
	if got := runTask("node.reprobe", id, "nrp-1"); got.Status != tasks.StatusSucceeded {
		t.Fatalf("node.reprobe: %s (%s)", got.Status, got.Error)
	}

	// An agent-side refusal records the agent's reason on the failed attempt
	// (the runner keeps retrying with backoff, so the recorded error — not the
	// terminal status — is the evidence here).
	failRestart.Store(true)
	rec2 := doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "agent.restart", "ref_id": id, "idempotency_key": "agr-2"})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("enqueue refused restart: %d %s", rec2.Code, rec2.Body)
	}
	var refRow storage.Task
	_ = json.Unmarshal(rec2.Body.Bytes(), &refRow)
	var refused storage.Task
	rDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(rDeadline) {
		mgr.LoopOnce(ctx)
		_ = db.First(&refused, refRow.ID).Error
		if strings.Contains(refused.Error, "refused") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	failRestart.Store(false)
	if !strings.Contains(refused.Error, "refused") {
		t.Fatalf("refused restart not recorded: %s %q", refused.Status, refused.Error)
	}
	for i := 0; i < 5; i++ { // stop the retry churn best-effort
		if rec := doJSON(t, r, "POST", "/api/tasks/"+itoa64(int(refRow.ID))+"/cancel", nil); rec.Code == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Unknown node refs fail the Depends precheck on the first attempt.
	rec3 := doJSON(t, r, "POST", "/api/tasks", map[string]any{"kind": "agent.restart", "ref_id": 99999})
	if rec3.Code != http.StatusCreated {
		t.Fatalf("enqueue unknown ref: %d %s", rec3.Code, rec3.Body)
	}
	var ghost storage.Task
	_ = json.Unmarshal(rec3.Body.Bytes(), &ghost)
	var dep storage.Task
	dDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(dDeadline) {
		mgr.LoopOnce(ctx)
		_ = db.First(&dep, ghost.ID).Error
		if dep.Error != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if dep.Stage != "depends" || dep.Error == "" {
		t.Fatalf("unknown-ref precheck: stage=%s err=%q", dep.Stage, dep.Error)
	}
	for i := 0; i < 5; i++ {
		if rec := doJSON(t, r, "POST", "/api/tasks/"+itoa64(int(ghost.ID))+"/cancel", nil); rec.Code == http.StatusOK {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}
