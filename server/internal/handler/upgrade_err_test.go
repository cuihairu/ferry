package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNodeUpgradeGuards covers bad ids on the self-upgrade entry (missing/invalid node) and
// the bad-payload 400 branches.
func TestNodeUpgradeGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "999"} {
		if rec := doJSON(t, r, "POST", "/api/nodes/"+id+"/upgrade",
			map[string]any{"version": "v2", "url": "https://example.com/a"}); rec.Code != http.StatusNotFound {
			t.Fatalf("node %s: %d, want 404", id, rec.Code)
		}
	}
	// The bad payload is validated after the node is located: persist a node first, then send bad JSON.
	id, _ := createNodeFor(t, r, "upg-json")
	if rec := doRaw(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}

// TestNodeUpgradeBadAck covers an agent ack payload that cannot be decoded -> 502.
func TestNodeUpgradeBadAck(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "upg-bad")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "upg-bad", token)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id),
			map[string]any{"version": "v2", "url": "https://example.com/a"})
	}()
	env := readEnv(t, c)
	// Reply with the same ID; the payload is valid JSON but its type cannot decode into UpgradeAck.
	bad := agentproto.Envelope{V: env.V, ID: env.ID, Type: agentproto.MsgUpgradeAck,
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

// TestNodeUpgradeTimeout covers an online agent that never acks -> 504 (the hub wait
// timeout branch). The wait comes from the upgradeAckTimeout constant; this test inherently takes ~15s.
func TestNodeUpgradeTimeout(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "upg-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "upg-to", token) // does not reply after the handshake

	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id),
		map[string]any{"version": "v2", "url": "https://example.com/a"})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
}
