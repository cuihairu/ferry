package handler

import (
	"net/http"
	"testing"
)

// TestSensitiveConfirm covers F-1709: destructive operations demand a
// server-side second confirmation — the request body must echo the resource
// name (or the fixed batch-stop token for a batch proc stop), not just ride a
// frontend dialog. The gate runs before the destructive work and answers 400
// confirmation mismatch on an absent or wrong confirm.
func TestSensitiveConfirm(t *testing.T) {
	r := newTestRouter(t)
	id, _ := createNodeFor(t, r, "conf-1")

	// Single proc stop: absent and wrong confirms are 400 before any agent
	// contact (the node is offline, so passing the gate surfaces as 502).
	rec := doJSON(t, r, "POST", "/api/nodes/1/proc", map[string]any{"action": "stop"})
	if rec.Code != http.StatusBadRequest || !containsStr(rec.Body.String(), "confirmation mismatch") {
		t.Fatalf("single stop no confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/nodes/1/proc", map[string]any{"action": "stop", "confirm": "wrong"})
	if rec.Code != http.StatusBadRequest || !containsStr(rec.Body.String(), "pass confirm=conf-1") {
		t.Fatalf("single stop wrong confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/nodes/1/proc", map[string]any{"action": "stop", "confirm": "conf-1"})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("single stop right confirm should pass the gate (offline 502): %d %s", rec.Code, rec.Body)
	}
	// start/reload stay ungated.
	rec = doJSON(t, r, "POST", "/api/nodes/1/proc", map[string]any{"action": "start"})
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("start must not demand confirm: %d %s", rec.Code, rec.Body)
	}

	// Batch proc stop: the fixed batch-stop token, absent/wrong 400 (the node
	// name is not accepted — the token names the batch scope, not one node).
	rec = doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{"ids": []uint{id}, "action": "stop"})
	if rec.Code != http.StatusBadRequest || !containsStr(rec.Body.String(), "pass confirm=batch-stop") {
		t.Fatalf("batch stop no confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{"ids": []uint{id}, "action": "stop", "confirm": "conf-1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("batch stop node-name confirm must not pass: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{"ids": []uint{id}, "action": "stop", "confirm": "batch-stop"})
	if rec.Code != http.StatusOK {
		t.Fatalf("batch stop with token: %d %s", rec.Code, rec.Body)
	}

	// Node deletion: echo the node name; the confirmed delete removes the row.
	rec = doJSON(t, r, "DELETE", "/api/nodes/1", nil)
	if rec.Code != http.StatusBadRequest || !containsStr(rec.Body.String(), "pass confirm=conf-1") {
		t.Fatalf("node delete no confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/nodes/1", map[string]any{"confirm": "conf-1"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("node delete with confirm: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/nodes/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted node should 404: %d", rec.Code)
	}

	// User deletion: echo the username; the confirmed delete removes the row.
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "conf-user"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/users/1", nil)
	if rec.Code != http.StatusBadRequest || !containsStr(rec.Body.String(), "pass confirm=conf-user") {
		t.Fatalf("user delete no confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/users/1", map[string]any{"confirm": "wrong"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("user delete wrong confirm: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/users/1", map[string]any{"confirm": "conf-user"})
	if rec.Code != http.StatusOK {
		t.Fatalf("user delete with confirm: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/users/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user should 404: %d", rec.Code)
	}
}
