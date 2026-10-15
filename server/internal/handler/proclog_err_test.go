package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNodeProcLogsGuards 覆盖日志拉取的坏 id 404 与缺 proc 400。
func TestNodeProcLogsGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/nodes/abc/logs?proc=xray", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	id, _ := createNodeFor(t, r, "pl-json")
	if rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs", id), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing proc: %d", rec.Code)
	}
}

// TestNodeProcLogsTimeout 覆盖 agent 在线但不回执 → 504（5s 超时分支）。
func TestNodeProcLogsTimeout(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "pl-to")
	srv := httptest.NewServer(r)
	defer srv.Close()
	fakeAgent(t, srv.URL, "pl-to", token) // 握手后不应答

	rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray", id), nil)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout should 504: %d %s", rec.Code, rec.Body)
	}
}

// TestNodeProcLogsBadAck 覆盖回执载荷无法解码 → 500。
func TestNodeProcLogsBadAck(t *testing.T) {
	r := newTestRouter(t)
	id, token := createNodeFor(t, r, "pl-bad")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "pl-bad", token)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/logs?proc=xray", id), nil)
	}()
	env := readEnv(t, c)
	// 同 ID 应答，载荷合法 JSON 但类型无法解进 ProcLogsAck。
	bad := agentproto.Envelope{V: env.V, ID: env.ID, Type: agentproto.MsgProcLogsAck,
		Payload: []byte(`"not-an-object"`)}
	if err := c.WriteJSON(bad); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("bad ack should 500: %d %s", rec.Code, rec.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bad ack timed out")
	}
}
