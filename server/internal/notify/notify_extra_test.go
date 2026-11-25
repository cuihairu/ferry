package notify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSendBadURL covers Send's NewRequest failure branch (an invalid URL).
func TestSendBadURL(t *testing.T) {
	n := &Notifier{url: "://bad-url", client: http.DefaultClient}
	if !n.Enabled() {
		t.Fatal("enabled")
	}
	if err := n.Send(Event{Event: "x"}); err == nil {
		t.Fatal("want error for bad url")
	}
}

// TestSendNon2xx covers a non-2xx status returning an error.
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

// TestSendConnectionError covers a client.Do network error.
func TestSendConnectionError(t *testing.T) {
	n := &Notifier{url: "http://127.0.0.1:1", client: http.DefaultClient}
	if err := n.Send(Event{Event: "x"}); err == nil {
		t.Fatal("want error for refused connection")
	}
}

// TestSendMarshalError an event with an unserializable field: packing errors out, without any network call.
func TestSendMarshalError(t *testing.T) {
	n := &Notifier{url: "http://127.0.0.1:1/hook", client: &http.Client{}}
	err := n.Send(Event{Event: "cost.high", Fields: map[string]any{"bad": make(chan int)}})
	if err == nil {
		t.Fatal("unmarshalable event field must error")
	}
}

// TestFromDBBadURL covers FromDB erroring while reading settings (an empty url returns disabled).
func TestFromDBBadURL(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, _ := db.DB(); s != nil {
			_ = s.Close()
		}
	})
	if n := FromDB(db); n.Enabled() {
		t.Fatal("unset should be disabled")
	}
}
