package handler

import (
	"net/http"
	"strings"
	"testing"

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
