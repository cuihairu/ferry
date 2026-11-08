package notify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSendBadURL 覆盖 Send 的 NewRequest 失败分支（非法 URL）。
func TestSendBadURL(t *testing.T) {
	n := &Notifier{url: "://bad-url", client: http.DefaultClient}
	if !n.Enabled() {
		t.Fatal("enabled")
	}
	if err := n.Send(Event{Event: "x"}); err == nil {
		t.Fatal("want error for bad url")
	}
}

// TestSendNon2xx 覆盖非 2xx 状态返回错误。
func TestSendNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	n := &Notifier{url: srv.URL, client: http.DefaultClient}
	if err := n.Send(Event{Event: "x"}); err == nil {
		t.Fatal("want error for 500")
	}
}

// TestSendConnectionError 覆盖 client.Do 网络错误。
func TestSendConnectionError(t *testing.T) {
	n := &Notifier{url: "http://127.0.0.1:1", client: http.DefaultClient}
	if err := n.Send(Event{Event: "x"}); err == nil {
		t.Fatal("want error for refused connection")
	}
}

// TestSendMarshalError 事件含不可序列化字段：组包即报错，不触网。
func TestSendMarshalError(t *testing.T) {
	n := &Notifier{url: "http://127.0.0.1:1/hook", client: &http.Client{}}
	err := n.Send(Event{Event: "cost.high", Fields: map[string]any{"bad": make(chan int)}})
	if err == nil {
		t.Fatal("unmarshalable event field must error")
	}
}

// TestFromDBBadURL 覆盖 FromDB 读设置出错（空 url 返回禁用）。
func TestFromDBBadURL(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { if s, _ := db.DB(); s != nil { _ = s.Close() } })
	if n := FromDB(db); n.Enabled() {
		t.Fatal("unset should be disabled")
	}
}
