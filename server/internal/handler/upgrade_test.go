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

// TestNodeUpgrade covers issuing the self-upgrade command (A-23): offline 502, parameter
// validation 400, agent acceptance 200, agent rejection 400.
func TestNodeUpgrade(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "upg-1")

	// offline 502
	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{
		"version": "v2", "url": "https://example.com/ferry-agent",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline should 502: %d %s", rec.Code, rec.Body)
	}
	// parameter validation
	rec = doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{"url": "https://x.com/a"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing version should 400: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{"version": "v2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url should 400: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{
		"version": "v2", "url": "not-a-url",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad url should 400: %d", rec.Code)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "upg-1", token)

	// agent accepts -> 200
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{
			"version": "v2", "url": "https://example.com/ferry-agent", "sha256": "abc",
		})
	}()
	env := readEnv(t, c)
	if env.Type != agentproto.MsgUpgrade {
		t.Fatalf("expected upgrade, got %s", env.Type)
	}
	var up agentproto.Upgrade
	if err := env.Decode(&up); err != nil {
		t.Fatalf("decode upgrade: %v", err)
	}
	if up.Version != "v2" || up.URL != "https://example.com/ferry-agent" || up.Sha256 != "abc" {
		t.Fatalf("upgrade payload mismatch: %+v", up)
	}
	replyAck(t, c, env, agentproto.MsgUpgradeAck, agentproto.UpgradeAck{Version: up.Version, OK: true})
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("upgrade: %d %s", rec.Code, rec.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out["accepted"] != true || out["version"] != "v2" {
			t.Fatalf("upgrade response mismatch: %v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upgrade request timed out")
	}

	// agent rejects -> 400
	go func() {
		done <- doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{
			"version": "v3", "url": "https://example.com/ferry-agent",
		})
	}()
	env = readEnv(t, c)
	replyAck(t, c, env, agentproto.MsgUpgradeAck, agentproto.UpgradeAck{Version: "v3", OK: false, Error: "disk full"})
	select {
	case rec := <-done:
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("rejected upgrade should 400: %d %s", rec.Code, rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rejected upgrade timed out")
	}
}

// TestAgentWSHelloVersion covers the agent version being persisted via hello (A-23 upgrade result check).
func TestAgentWSHelloVersion(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "upg-2")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "upg-2", token)
	_ = c.Close() // fakeAgent already carries a version, so just assert the persisted value

	var node struct {
		AgentVersion string
	}
	row := db.Model(&storage.Node{}).Select("agent_version").Where("id = ?", id).Row()
	if err := row.Scan(&node.AgentVersion); err != nil {
		t.Fatalf("scan agent_version: %v", err)
	}
	if node.AgentVersion != "test" {
		t.Fatalf("agent_version = %q, want test", node.AgentVersion)
	}
}
