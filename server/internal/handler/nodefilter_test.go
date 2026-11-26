package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeFilters covers F-1007~F-1010: GET /api/nodes narrows the node list by
// capability, region, ISP/ASN and line type; each filter is independent, they
// AND together, and an unmatched value yields an empty list rather than an
// error.
func TestNodeFilters(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string, mutate func(*storage.Node)) uint {
		n := storage.Node{Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless", Token: name, Enabled: true}
		if mutate != nil {
			mutate(&n)
		}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	// entry: relay_entry, 华东/电信/AS4134, CN2-GIA.
	mkNode("entry-hd", func(n *storage.Node) {
		n.Role = "entry"
		n.Region = "华东"
		n.ISP = "电信"
		n.ASN = "AS4134"
		n.LineType = "cn2_gia"
		n.Capabilities = `["proxy_ingress","relay_entry","probe"]`
	})
	// landing: relay_landing, 华东/联通/AS4837, CMI.
	mkNode("landing-cu", func(n *storage.Node) {
		n.Role = "landing"
		n.Region = "华东"
		n.ISP = "联通"
		n.ASN = "AS4837"
		n.LineType = "cmi"
		n.Capabilities = `["relay_landing"]`
	})
	// landing in another region, plain line.
	mkNode("landing-sx", func(n *storage.Node) {
		n.Role = "landing"
		n.Region = "东南亚"
		n.ISP = "移动"
		n.ASN = "AS9808"
		n.LineType = "普通"
		n.Capabilities = `["relay_landing"]`
	})

	list := func(params map[string]string) []map[string]any {
		t.Helper()
		q := url.Values{}
		for k, v := range params {
			q.Set(k, v)
		}
		path := "/api/nodes"
		if enc := q.Encode(); enc != "" {
			path += "?" + enc
		}
		rec := doJSON(t, r, "GET", path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		var out []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	hasName := func(out []map[string]any, name string) bool {
		for _, row := range out {
			if row["name"] == name {
				return true
			}
		}
		return false
	}

	// No filter: every node.
	if out := list(nil); len(out) != 3 {
		t.Fatalf("unfiltered count = %d", len(out))
	}

	// F-1007 capability: an entry-side filter must not return a landing-only
	// node. The static list is authoritative; role-derived caps apply when no
	// list is stored.
	if out := list(map[string]string{"capability": "relay_entry"}); len(out) != 1 || !hasName(out, "entry-hd") {
		t.Fatalf("capability=relay_entry = %v", out)
	}
	if out := list(map[string]string{"capability": "relay_landing"}); len(out) != 2 {
		t.Fatalf("capability=relay_landing count = %d", len(out))
	}

	// F-1008 region.
	if out := list(map[string]string{"region": "华东"}); len(out) != 2 {
		t.Fatalf("region count = %d", len(out))
	}
	if out := list(map[string]string{"region": "欧洲"}); len(out) != 0 {
		t.Fatalf("unknown region should be empty, got %d", len(out))
	}

	// F-1009 ISP and ASN are separate filters.
	if out := list(map[string]string{"isp": "联通"}); len(out) != 1 || !hasName(out, "landing-cu") {
		t.Fatalf("isp filter = %v", out)
	}
	if out := list(map[string]string{"asn": "AS9808"}); len(out) != 1 || !hasName(out, "landing-sx") {
		t.Fatalf("asn filter = %v", out)
	}

	// F-1010 line type.
	if out := list(map[string]string{"line_type": "cn2_gia"}); len(out) != 1 || !hasName(out, "entry-hd") {
		t.Fatalf("line_type filter = %v", out)
	}

	// Combined filters AND together.
	if out := list(map[string]string{"region": "华东", "line_type": "cn2_gia"}); len(out) != 1 || !hasName(out, "entry-hd") {
		t.Fatalf("combined filter = %v", out)
	}
	if out := list(map[string]string{"region": "华东", "isp": "移动"}); len(out) != 0 {
		t.Fatalf("impossible combo should be empty, got %d", len(out))
	}

	// An unknown capability name is a bad request, not a silent empty list.
	rec := doJSON(t, r, "GET", "/api/nodes?capability=vending", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown capability = %d %s", rec.Code, rec.Body)
	}
}
