package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestAPIMetrics covers F-1801: the /api group counts requests and latency per
// route template and status code, and GET /admin/metrics exposes the
// registry — admin-only.
func TestAPIMetrics(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)

	ok200 := apiRequests.WithLabelValues("GET", "/api/nodes", "200")
	unauth401 := apiRequests.WithLabelValues("GET", "/api/nodes", "401")
	before200 := testutil.ToFloat64(ok200)
	before401 := testutil.ToFloat64(unauth401)

	doJSON(t, r, "GET", "/api/nodes", nil)
	doJSON(t, r, "GET", "/api/nodes", nil)
	doBare(t, r, "GET", "/api/nodes") // auth failure is counted too

	if got := testutil.ToFloat64(ok200) - before200; got != 2 {
		t.Fatalf("200 counter delta = %v, want 2", got)
	}
	if got := testutil.ToFloat64(unauth401) - before401; got != 1 {
		t.Fatalf("401 counter delta = %v, want 1", got)
	}

	// Latency histogram observes the same routes.
	rec := doJSON(t, r, "GET", "/admin/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `ferry_api_requests_total{code="200",method="GET",route="/api/nodes"}`) {
		t.Fatal("request counter line missing from exposition")
	}
	if !strings.Contains(body, `ferry_api_request_seconds_count{method="GET",route="/api/nodes"}`) {
		t.Fatal("latency histogram line missing from exposition")
	}
	if !strings.Contains(body, `ferry_api_request_seconds_bucket`) {
		t.Fatal("latency buckets missing from exposition")
	}

	// The exposition is not public.
	if rec := doBare(t, r, "GET", "/admin/metrics"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed metrics: %d", rec.Code)
	}
}

// TestDBMetrics covers F-1802: pool gauges answer scrapes from sql.DBStats,
// statement latency lands per operation class, injected failures count while
// routine record-not-found misses do not.
func TestDBMetrics(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)

	rec := doJSON(t, r, "GET", "/admin/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"ferry_db_open_connections",
		"ferry_db_in_use_connections",
		"ferry_db_idle_connections",
		"ferry_db_connection_waits_total",
		"ferry_db_connection_wait_seconds_total",
		`ferry_db_query_seconds_count{op="query"}`,
		`ferry_db_query_seconds_count{op="create"}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("exposition missing %s", want)
		}
	}

	// Injected query failure: the error counter moves by exactly one; a
	// record-not-found miss afterwards does not.
	errChild := dbQueryErrors.WithLabelValues("query")
	before := testutil.ToFloat64(errChild)
	regQueryFail("t:dbm", db, (*[]storage.NodeTrafficLog)(nil))
	if rec := doJSON(t, r, "GET", "/api/nodes/1/traffic-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("injected failure: %d %s", rec.Code, rec.Body)
	}
	if got := testutil.ToFloat64(errChild) - before; got != 1 {
		t.Fatalf("error counter delta = %v, want 1", got)
	}
	if rec := doJSON(t, r, "GET", "/api/nodes/999999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing node: %d", rec.Code)
	}
	if got := testutil.ToFloat64(errChild) - before; got != 1 {
		t.Fatalf("record-not-found must not count as error: delta %v", got)
	}
}

// TestAgentMetrics covers F-1803: the online gauge answers the hub live, and
// per-node heartbeat ages (plus the max) render for online nodes only.
func TestAgentMetrics(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)

	// Baseline: nobody online.
	rec := doJSON(t, r, "GET", "/admin/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "ferry_agent_online 0") {
		t.Fatalf("empty fleet must report 0 online: %s", rec.Body)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	id, token := createNodeFor(t, r, "agm-1")
	c := fakeAgent(t, srv.URL, "agm-1", token)

	// Heartbeat: the server stamps last_seen before the ack comes back, so the
	// age series is readable right after the ack.
	hb, _ := agentproto.NewEnvelope("hb1", agentproto.MsgHeartbeat, agentproto.Heartbeat{})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}

	rec = doJSON(t, r, "GET", "/admin/metrics", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "ferry_agent_online 1") {
		t.Fatalf("online agent must report 1: %s", body)
	}
	if !strings.Contains(body, `ferry_agent_heartbeat_age_seconds{node_id="`+itoa64(int(id))+`"}`) {
		t.Fatalf("per-node heartbeat age missing: %s", body)
	}
	if !strings.Contains(body, "ferry_agent_heartbeat_age_max_seconds") {
		t.Fatalf("max heartbeat age missing: %s", body)
	}
}

// TestRelayMetrics covers F-1804: the relay fleet and online gauges answer
// from relay_caps declarations intersected with the hub, and the connection
// gauge reads the newest per-node traffic sample.
func TestRelayMetrics(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)

	srv := httptest.NewServer(r)
	defer srv.Close()
	relayID, token := createNodeFor(t, r, "rly-1")
	plainID, plainToken := createNodeFor(t, r, "rly-plain")
	if err := db.Model(&storage.Node{}).Where("id = ?", relayID).
		Update("relay_caps", `{"tcp_forward":true}`).Error; err != nil {
		t.Fatal(err)
	}
	// Both agents online: only the relay-declaring one counts as relay online.
	ca := fakeAgent(t, srv.URL, "rly-1", token)
	_ = fakeAgent(t, srv.URL, "rly-plain", plainToken)
	_ = ca

	// Traffic samples: an older 3-conn row and the newest 7-conn row — the
	// newest must win; a non-relay proc sample must be ignored. The rx/tx
	// deltas sum across rows: 100+50 / 200+60.
	now := time.Now()
	for _, s := range []storage.NodeTrafficLog{
		{NodeID: relayID, Proc: "relay", Conns: 3, RxBytes: 100, TxBytes: 200, RecordedAt: now.Add(-time.Minute)},
		{NodeID: relayID, Proc: "relay", Conns: 7, RxBytes: 50, TxBytes: 60, RecordedAt: now},
		{NodeID: relayID, Proc: "xray", Conns: 99, RxBytes: 1 << 20, TxBytes: 1 << 20, RecordedAt: now},
	} {
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, r, "GET", "/admin/metrics", nil)
	body := rec.Body.String()
	if !strings.Contains(body, "ferry_relay_nodes 1") {
		t.Fatalf("relay fleet must count only declarations: %s", body)
	}
	if !strings.Contains(body, "ferry_relay_online 1") {
		t.Fatalf("relay online must intersect the hub: %s", body)
	}
	if !strings.Contains(body, `ferry_relay_connections{node_id="`+itoa64(int(relayID))+`"} 7`) {
		t.Fatalf("relay conns must read the newest sample: %s", body)
	}
	if !strings.Contains(body, `ferry_relay_rx_bytes_total{node_id="`+itoa64(int(relayID))+`"} 150`) {
		t.Fatalf("relay rx total must sum deltas: %s", body)
	}
	if !strings.Contains(body, `ferry_relay_tx_bytes_total{node_id="`+itoa64(int(relayID))+`"} 260`) {
		t.Fatalf("relay tx total must sum deltas: %s", body)
	}
	if strings.Contains(body, `ferry_relay_connections{node_id="`+itoa64(int(plainID))+`"}`) {
		t.Fatal("non-relay proc samples must not appear")
	}
	if strings.Contains(body, `ferry_relay_rx_bytes_total{node_id="`+itoa64(int(plainID))+`"}`) {
		t.Fatal("non-relay bytes must not appear")
	}
}
