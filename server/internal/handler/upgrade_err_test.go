package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNodeUpgradeGuards 覆盖自升级入口的坏 id（节点不存在/非法）与
// 坏载荷 400 分支。
func TestNodeUpgradeGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, id := range []string{"abc", "999"} {
		if rec := doJSON(t, r, "POST", "/api/nodes/"+id+"/upgrade",
			map[string]any{"version": "v2", "url": "https://example.com/a"}); rec.Code != http.StatusNotFound {
			t.Fatalf("node %s: %d, want 404", id, rec.Code)
		}
	}
	// 坏载荷在节点定位之后校验：先落一个节点再发坏 JSON。
	id, _ := createNodeFor(t, r, "upg-json")
	if rec := doRaw(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
}

// TestNodeUpgradeBadAck 覆盖 agent 回执载荷无法解码 → 502。
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
	// 同 ID 应答，载荷合法 JSON 但类型无法解进 UpgradeAck。
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

// TestNodeUpgradeTimeout 覆盖 agent 在线但不回执 → 504（hub 等待超时分
// 支）。等待时长取自 upgradeAckTimeout 常量，本用例固有耗时约 15s。
func TestNodeUpgradeTimeout(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "upg-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "upg-to", token) // 握手后不应答

	rec := doJSON(t, r, "POST", fmt.Sprintf("/api/nodes/%d/upgrade", id),
		map[string]any{"version": "v2", "url": "https://example.com/a"})
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
}
