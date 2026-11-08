package handler

import (
	"net/http"
	"testing"
)

// TestNodeTokenEndpointsNotFound 删除/轮换/吊销都查无此节点：404 统一口径。
func TestNodeTokenEndpointsNotFound(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	cases := []struct{ method, path string }{
		{"DELETE", "/api/nodes/99999"},
		{"POST", "/api/nodes/99999/token"},
		{"POST", "/api/nodes/99999/token/revoke"},
	}
	for _, c := range cases {
		if rec := doJSON(t, r, c.method, c.path, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d %s; want 404", c.method, c.path, rec.Code, rec.Body)
		}
	}
}
