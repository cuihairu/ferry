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

// TestNodeUpgrade 覆盖自升级指令下发（A-23）：离线 502、参数校验 400、
// agent 受理 200、agent 拒绝 400。
func TestNodeUpgrade(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "upg-1")

	// 离线 502
	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), map[string]any{
		"version": "v2", "url": "https://example.com/ferry-agent",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline should 502: %d %s", rec.Code, rec.Body)
	}
	// 参数校验
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

	// agent 受理 → 200
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

	// agent 拒绝 → 400
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

// TestAgentWSHelloVersion 覆盖 agent 版本随 hello 落库（A-23 升级结果核对）。
func TestAgentWSHelloVersion(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "upg-2")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "upg-2", token)
	_ = c.Close() // fakeAgent 已带版本，直接断言落库

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
