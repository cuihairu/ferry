package handler

import (
	"net/http"
	"strings"
	"testing"

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
