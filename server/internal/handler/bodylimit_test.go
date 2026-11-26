package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAPIBodyLimit covers the /api request-body cap (F-0728): a body over the
// limit fails the bind instead of buffering unbounded bytes.
func TestAPIBodyLimit(t *testing.T) {
	r := newTestRouter(t)
	big := `{"name":"` + strings.Repeat("x", 9<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/nodes", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-limit body must fail: code=%d", rec.Code)
	}

	// a normal small write still works.
	rec = doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "bl-1", "address": "bl.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("normal create: %d %s", rec.Code, rec.Body)
	}
}
