package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeConfigGuards covers the guard branches for config pushes and node
// traffic/config history: bad id 404, bad payload 400, missing required fields
// 400, and a legal limit taking effect on the config history.
func TestNodeConfigGuards(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "POST", "/api/nodes/abc/config", map[string]any{"proc": "xray"}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	if rec := doRaw(t, r, "POST", "/api/nodes/1/config", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/1/config", map[string]any{"proc": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing fields: %d", rec.Code)
	}

	// A legal limit on the config history takes effect (the explicit-value path other than the default 50).
	r2, db2 := newTestRouterWithDB(t)
	nid, _ := createNodeFor(t, r2, "cfg-lim")
	for i := 0; i < 3; i++ {
		if err := db2.Create(&storage.NodeConfig{NodeID: nid, Proc: "xray",
			Version: "v", Sha256: "s", Payload: "p", Status: "applied"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if rec := doJSON(t, r2, "GET", "/api/nodes/"+itoa64(int(nid))+"/configs?limit=2", nil); rec.Code != http.StatusOK {
		t.Fatalf("configs: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r2, "GET", "/api/nodes/abc/configs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("configs bad id: %d", rec.Code)
	}
}

// TestNodeTrafficLogsErrors covers the node traffic accounting guards and fetch
// failures: bad id 404, illegal limit 400, fetch failure 500; config history
// fetch failure 500.
func TestNodeTrafficLogsErrors(t *testing.T) {
	// Bad id -> 404.
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/nodes/abc/traffic-logs", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// Illegal limit -> 400.
	if rec := doJSON(t, r, "GET", "/api/nodes/1/traffic-logs?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: %d", rec.Code)
	}

	// Traffic accounting fetch failure -> 500.
	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:ntl", db2, (*[]storage.NodeTrafficLog)(nil))
	if rec := doJSON(t, r2, "GET", "/api/nodes/1/traffic-logs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("traffic rows: %d %s", rec.Code, rec.Body)
	}

	// Config history fetch failure -> 500.
	r3, db3 := newTestRouterWithDB(t)
	regQueryFail("t:ncf", db3, (*[]storage.NodeConfig)(nil))
	if rec := doJSON(t, r3, "GET", "/api/nodes/1/configs", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("config rows: %d %s", rec.Code, rec.Body)
	}
}
