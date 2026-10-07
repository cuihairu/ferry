package herald

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestHTTPSender 覆盖投递腿：路径/Bearer 头/载荷格式（含 id 扩展与 Meta
// 透传）、2xx 成功、非 2xx 报错（进退避重试）。
func TestHTTPSender(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotPayload outgoingEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotPayload)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	ev := storage.Event{
		ID: 42, Kind: "region_fault", Severity: SeverityCritical,
		Title: "华东不可达", Body: "3/3 sick", Target: "admin",
		DedupKey: "region:华东", Meta: `{"count":3}`,
		OccurredAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	}
	send := HTTPSender(srv.URL, "tok-1")
	if err := send(ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotPath != "/events" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-1" || gotCT != "application/json" {
		t.Fatalf("headers = %q / %q", gotAuth, gotCT)
	}
	if gotPayload.ID != 42 || gotPayload.Kind != "region_fault" || gotPayload.Severity != SeverityCritical ||
		gotPayload.DedupKey != "region:华东" || gotPayload.Target != "admin" {
		t.Fatalf("payload = %+v", gotPayload)
	}
	if string(gotPayload.Meta) != `{"count":3}` {
		t.Fatalf("meta = %q", gotPayload.Meta)
	}
	if gotPayload.OccurredAt.IsZero() {
		t.Fatal("occurred_at missing")
	}

	// 无令牌不带 Authorization 头
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("empty token should not set Authorization")
		}
	}))
	defer srv2.Close()
	if err := HTTPSender(srv2.URL, "")(ev); err != nil {
		t.Fatalf("tokenless send: %v", err)
	}

	// 5xx 报错（由 Deliver 走退避）
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv3.Close()
	if err := HTTPSender(srv3.URL, "t")(ev); err == nil {
		t.Fatal("503 should be an error")
	}
	// 尾斜线 URL 去重
	if err := HTTPSender(srv.URL+"/", "t")(ev); err != nil {
		t.Fatalf("trailing slash url: %v", err)
	}
}
