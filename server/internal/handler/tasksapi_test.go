package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

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
