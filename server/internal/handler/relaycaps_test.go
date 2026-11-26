package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestHeartbeatRelayCapsPersistence covers F-0804's server side: a heartbeat
// carrying RelayCaps persists the JSON on the node row, GET /api/nodes/:id/status
// surfaces it as a structured object, a heartbeat without the field leaves the
// stored declaration intact (old agents must not erase it), and a node that
// never reports caps returns null (never invented).
func TestHeartbeatRelayCapsPersistence(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "rcaps-1")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "rcaps-1", token)

	sendHB := func(caps *agentproto.RelayCaps) {
		t.Helper()
		env, _ := agentproto.NewEnvelope(fmt.Sprintf("hb-%d", time.Now().UnixNano()),
			agentproto.MsgHeartbeat, agentproto.Heartbeat{RelayCaps: caps, At: time.Now()})
		if err := c.WriteJSON(env); err != nil {
			t.Fatalf("write heartbeat: %v", err)
		}
		if got := readEnv(t, c); got.Type != agentproto.MsgHeartbeatAck {
			t.Fatalf("expected heartbeat_ack, got %s", got.Type)
		}
	}

	sendHB(&agentproto.RelayCaps{
		TCPForward: true, Reconnect: true, IdleCleanup: true, Metrics: true,
		StreamLimit: true, BandwidthLimit: true, MaxConns: 64, MaxStreams: 256, RateLimitKbps: 2048,
	})
	var n storage.Node
	if err := db.First(&n, id).Error; err != nil {
		t.Fatalf("load node: %v", err)
	}
	if n.RelayCaps == "" {
		t.Fatal("relay_caps column empty after heartbeat")
	}

	// GET status surfaces the structured declaration.
	rec := doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/status", id), nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var st struct {
		RelayCaps *struct {
			TCPForward     bool `json:"tcp_forward"`
			UDPForward     bool `json:"udp_forward"`
			Reconnect      bool `json:"reconnect"`
			IdleCleanup    bool `json:"idle_cleanup"`
			Metrics        bool `json:"metrics"`
			StreamLimit    bool `json:"stream_limit"`
			BandwidthLimit bool `json:"bandwidth_limit"`
			MaxConns       int  `json:"max_conns"`
			MaxStreams     int  `json:"max_streams"`
			RateLimitKbps  int  `json:"rate_limit_kbps"`
		} `json:"relay_caps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("status decode: %v", err)
	}
	if st.RelayCaps == nil {
		t.Fatalf("status relay_caps null: %s", rec.Body.String())
	}
	if !st.RelayCaps.TCPForward || !st.RelayCaps.Reconnect || st.RelayCaps.UDPForward ||
		!st.RelayCaps.IdleCleanup || !st.RelayCaps.Metrics || !st.RelayCaps.StreamLimit ||
		!st.RelayCaps.BandwidthLimit || st.RelayCaps.MaxConns != 64 ||
		st.RelayCaps.MaxStreams != 256 || st.RelayCaps.RateLimitKbps != 2048 {
		t.Fatalf("relay caps mismatch: %+v", st.RelayCaps)
	}

	// A heartbeat without the field must not erase the stored declaration.
	sendHB(nil)
	var n2 storage.Node
	if err := db.First(&n2, id).Error; err != nil {
		t.Fatalf("reload node: %v", err)
	}
	if n2.RelayCaps != n.RelayCaps {
		t.Fatalf("caps erased by a heartbeat without the field: %q vs %q", n2.RelayCaps, n.RelayCaps)
	}

	// A node that never reported caps: omitted in the aggregate, never invented.
	createNodeFor(t, r, "rcaps-none")
	var nodes []storage.Node
	if err := db.Find(&nodes).Error; err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	var otherID uint
	for _, nd := range nodes {
		if nd.Name == "rcaps-none" {
			otherID = nd.ID
		}
	}
	if otherID == 0 {
		t.Fatal("rcaps-none node not found")
	}
	rec = doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/status", otherID), nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var bare map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &bare); err != nil {
		t.Fatalf("bare status decode: %v", err)
	}
	if _, ok := bare["relay_caps"]; ok {
		t.Fatalf("node without reported caps must omit relay_caps: %s", rec.Body.String())
	}
}
