package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cfFake 是最小 Cloudflare API 替身：zone 按名返回、记录 upsert 留痕。
type cfFake struct {
	zone     string  // 托管 zone 名（如 example.com）
	records  []cfRec // 既有记录
	posts    []map[string]any
	puts     []map[string]any
	zoneGets []string
}

type cfRec struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (f *cfFake) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
			return
		}
		write := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": v})
		}
		path := r.URL.Path
		switch {
		case path == "/zones":
			name := r.URL.Query().Get("name")
			f.zoneGets = append(f.zoneGets, name)
			if name == f.zone {
				write([]map[string]string{{"id": "zid1"}})
				return
			}
			write([]any{})
		case strings.HasPrefix(path, "/zones/zid1/dns_records") && r.Method == http.MethodGet:
			write(f.records)
		case strings.HasPrefix(path, "/zones/zid1/dns_records/") && r.Method == http.MethodPut:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.puts = append(f.puts, body)
			write(map[string]string{"id": "rid1"})
		case path == "/zones/zid1/dns_records" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.posts = append(f.posts, body)
			write(map[string]string{"id": "rid2"})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"not found"}]}`))
		}
	})
}

func newFake(t *testing.T) (*Cloudflare, *cfFake) {
	t.Helper()
	f := &cfFake{zone: "example.com"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &Cloudflare{Token: "tok-test", Base: srv.URL, HTTP: srv.Client()}, f
}

// TestCloudflareUpsertCreate 未命中记录时 POST 创建，记录名整名写入。
func TestCloudflareUpsertCreate(t *testing.T) {
	c, f := newFake(t)
	if err := c.Upsert(context.Background(), "edge.example.com", "A", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 || len(f.puts) != 0 {
		t.Fatalf("posts=%d puts=%d", len(f.posts), len(f.puts))
	}
	if f.posts[0]["content"] != "1.2.3.4" || f.posts[0]["name"] != "edge.example.com" {
		t.Fatalf("post = %+v", f.posts[0])
	}
}

// TestCloudflareUpsertUpdate 命中既有记录时 PUT 更新，并保持 proxied 状态。
func TestCloudflareUpsertUpdate(t *testing.T) {
	c, f := newFake(t)
	f.records = []cfRec{{ID: "rid1", Type: "A", Name: "edge.example.com", Content: "9.9.9.9", Proxied: true}}
	if err := c.Upsert(context.Background(), "edge.example.com", "A", "5.6.7.8"); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 1 || len(f.posts) != 0 {
		t.Fatalf("posts=%d puts=%d", len(f.posts), len(f.puts))
	}
	if f.puts[0]["content"] != "5.6.7.8" || f.puts[0]["proxied"] != true {
		t.Fatalf("put = %+v", f.puts[0])
	}
}

// TestCloudflareZoneWalk 子域逐级向上定位到 example.com 的 zone。
func TestCloudflareZoneWalk(t *testing.T) {
	c, f := newFake(t)
	if err := c.Upsert(context.Background(), "a.b.example.com", "A", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if len(f.zoneGets) < 2 || f.zoneGets[0] != "a.b.example.com" || f.zoneGets[len(f.zoneGets)-1] != "example.com" {
		t.Fatalf("zoneGets = %v", f.zoneGets)
	}
}

// TestCloudflareNoZone zone 一路向上找不到：报错不冒称成功。
func TestCloudflareNoZone(t *testing.T) {
	c, _ := newFake(t)
	err := c.Upsert(context.Background(), "edge.other.net", "A", "1.1.1.1")
	if err == nil || !strings.Contains(err.Error(), "no zone") {
		t.Fatalf("err = %v", err)
	}
}

// TestNewKinds 插件位登记口径：已接入造实例，未知类型不冒称支持。
func TestNewKinds(t *testing.T) {
	if _, err := New("cloudflare", "tok"); err != nil {
		t.Fatal(err)
	}
	if KnownKind("cloudflare") != true || KnownKind("alidns") != false {
		t.Fatal("KnownKind mismatch")
	}
	if _, err := New("alidns", "tok"); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if _, err := New("cloudflare", ""); err == nil {
		t.Fatal("empty token must fail")
	}
}
