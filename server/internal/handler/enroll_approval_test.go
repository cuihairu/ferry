package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// TestEnrollmentApproval covers the F-0741 flow: a require-approval credential
// enrolls into a disabled pending node whose hello is refused; approve activates
// it, reject marks it refused and can no longer be approved.
func TestEnrollmentApproval(t *testing.T) {
	r := newTestRouter(t)

	// Issue with require_approval and redeem it.
	tok, _ := enrollIssueToken(t, r, map[string]any{"require_approval": true})
	rec := doEnroll(t, r, tok, enrollValidNodeBody())
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("approval enroll: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	if node["enabled"] != false {
		t.Fatalf("approval node must start disabled: %s", rec.Body)
	}
	if node["provision_note"] != "pending approval" {
		t.Fatalf("pending note missing: %s", rec.Body)
	}
	id := utoa(uint(node["id"].(float64)))
	token := node["token"].(string)

	// hello with the pending node's token must be refused (authNode gates on enabled).
	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "hk-1", Version: "test",
	})
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("pending node hello must be refused")
	}
	c.Close()

	// Approve flips it active.
	rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+id+"/approve", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "pending approval") {
		t.Fatalf("approve must clear the note: %s", rec.Body)
	}

	// After approval the same token authenticates.
	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial after approve: %v", err)
	}
	defer c2.Close()
	hello, _ = agentproto.NewEnvelope("h2", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "hk-1", Version: "test",
	})
	if err := c2.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c2); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack after approve, got %s", env.Type)
	}

	// A second approval flow: reject marks refused and approve is refused too.
	tok2, _ := enrollIssueToken(t, r, map[string]any{"require_approval": true})
	rec = doEnroll(t, r, tok2, map[string]any{
		"name": "hk-2", "address": "hk2.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second enroll: %d %s", rec.Code, rec.Body)
	}
	var node2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node2)
	id2 := utoa(uint(node2["id"].(float64)))
	rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+id2+"/reject", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"rejected"`) {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+id2+"/approve", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("approve after reject must fail: %d %s", rec.Code, rec.Body)
	}
	// A node outside the flow is not approvable either.
	if rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+id+"/approve", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("re-approve must fail: %d", rec.Code)
	}
}

// TestEnrollmentNoApprovalKeepsBehavior pins the default: a credential without
// require_approval still enrolls straight into an enabled node.
func TestEnrollmentNoApprovalKeepsBehavior(t *testing.T) {
	r := newTestRouter(t)
	tok, _ := enrollIssueToken(t, r, map[string]any{})
	rec := doEnroll(t, r, tok, enrollValidNodeBody())
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("plain enroll: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"pending"`) {
		t.Fatalf("plain enroll must not be pending: %s", rec.Body)
	}
}
