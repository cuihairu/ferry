package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNodeProcOpGuards covers single-node process operation guards: bad id
// (node missing/invalid) and bad payload -> 400.
func TestNodeProcOpGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "999"} {
		if rec := doJSON(t, r, "POST", "/api/nodes/"+id+"/proc",
			map[string]any{"action": "reload"}); rec.Code != http.StatusNotFound {
			t.Fatalf("node %s: %d, want 404", id, rec.Code)
		}
	}
	// Payload validation happens after node lookup: create a node first, then send bad JSON.
	id, _ := createNodeFor(t, r, "ctl-json")
	if rec := doRaw(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}

// TestBatchProcGuards covers the batch process operation and batch config dispatch
// 400 branches: bad payload, empty ids, missing required fields.
func TestBatchProcGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, "POST", "/api/nodes/batch/proc", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("batch proc bad json: %d", rec.Code)
	}
	if rec := doRaw(t, r, "POST", "/api/nodes/batch/config", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("batch config bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/batch/config", map[string]any{
		"ids": []uint{}, "proc": "xray", "payload": `{}`,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("batch config empty ids: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/batch/config", map[string]any{
		"ids": []uint{1}, "proc": "", "payload": `{}`,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("batch config missing proc: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/batch/config", map[string]any{
		"ids": []uint{1}, "proc": "xray",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("batch config missing payload: %d", rec.Code)
	}
}

// TestProcOpTimeout covers the agent being online but never acknowledging -> 504 (hub wait timeout branch).
func TestProcOpTimeout(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "ctl-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "ctl-to", token) // handshake done but no reply

	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id),
		map[string]any{"action": "reload"})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
}

// TestProcOpBadAck covers an undecodable agent ack payload -> 502.
func TestProcOpBadAck(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "ctl-bad")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "ctl-bad", token)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/proc", id),
			map[string]any{"action": "reload"})
	}()
	env := readEnv(t, c)
	// Reply with the same ID; payload is valid JSON but its type cannot decode into ProcCtlAck.
	bad := agentproto.Envelope{V: env.V, ID: env.ID, Type: agentproto.MsgProcCtlAck,
		Payload: []byte(`"not-an-object"`)}
	if err := c.WriteJSON(bad); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("bad ack should 502: %d %s", rec.Code, rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bad ack timed out")
	}
}

// TestBatchProcAckError covers the batch process case where an online node's ack
// carries an error (per-node error reply, overall status still 200).
func TestBatchProcAckError(t *testing.T) {
	r := newTestRouter(t)
	idA, tokenA := createNodeFor(t, r, "bpe-a")
	idB, tokenB := createNodeFor(t, r, "bpe-b")
	srv := httptest.NewServer(r)
	defer srv.Close()
	ca := fakeAgent(t, srv.URL, "bpe-a", tokenA)
	cb := fakeAgent(t, srv.URL, "bpe-b", tokenB)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", "/api/nodes/batch/proc", map[string]any{
			"ids": []uint{idA, idB}, "action": "stop",
		})
	}()

	envA := readEnv(t, ca)
	replyAck(t, ca, envA, agentproto.MsgProcCtlAck,
		agentproto.ProcCtlAck{Proc: "xray", Action: "stop", OK: true})
	envB := readEnv(t, cb)
	replyAck(t, cb, envB, agentproto.MsgProcCtlAck,
		agentproto.ProcCtlAck{Proc: "xray", Action: "stop", Error: `unknown proc "xray"`})

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("batch proc: %d %s", rec.Code, rec.Body)
		}
		var out []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Fatalf("results = %d, want 2", len(out))
		}
		byID := map[uint]map[string]any{}
		for _, row := range out {
			byID[uint(row["node_id"].(float64))] = row
		}
		if byID[idA]["ok"] != true {
			t.Fatalf("ok node should pass: %v", byID[idA])
		}
		if byID[idB]["ok"] != false {
			t.Fatalf("error ack node should fail: %v", byID[idB])
		}
		if s, _ := byID[idB]["error"].(string); s != `unknown proc "xray"` {
			t.Fatalf("error passthrough = %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch ack error timed out")
	}
}
