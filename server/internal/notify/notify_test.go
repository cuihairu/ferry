package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestFromDBUnconfigured 未配置渠道时为空操作。
func TestFromDBUnconfigured(t *testing.T) {
	db := testDB(t)
	n := FromDB(db)
	if n.Enabled() {
		t.Fatal("unconfigured notifier should be disabled")
	}
	if err := n.Send(Event{Event: "x", Text: "y"}); err != nil {
		t.Fatalf("send on disabled: %v", err)
	}
}

// TestSendWebhook 配置后外发：JSON 载荷与密钥头齐全，接收端 500 报错。
func TestSendWebhook(t *testing.T) {
	db := testDB(t)
	var gotHeader http.Header
	body := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		buf, _ := io.ReadAll(r.Body)
		body <- buf
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := storage.SetSetting(db, KeyURL, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetSetting(db, KeySecret, "s3cret"); err != nil {
		t.Fatal(err)
	}

	n := FromDB(db)
	if !n.Enabled() {
		t.Fatal("configured notifier should be enabled")
	}
	before := time.Now()
	if err := n.Send(Event{Event: "review.disabled", Text: "停用 2 个", Fields: map[string]any{"count": 2}}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case buf := <-body:
		if gotHeader.Get("X-Ferry-Webhook-Secret") != "s3cret" {
			t.Fatalf("secret header = %q", gotHeader.Get("X-Ferry-Webhook-Secret"))
		}
		if ct := gotHeader.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content type = %q", ct)
		}
		var ev Event
		if err := json.Unmarshal(buf, &ev); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ev.Event != "review.disabled" || ev.Text != "停用 2 个" {
			t.Fatalf("event mismatch: %+v", ev)
		}
		if ev.Time.Before(before.Add(-time.Second)) || ev.Time.After(time.Now().Add(time.Second)) {
			t.Fatalf("time not filled: %v", ev.Time)
		}
		if ev.Fields["count"].(float64) != 2 {
			t.Fatalf("fields mismatch: %v", ev.Fields)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not hit")
	}

	// 接收端报错 → Send 返回错误
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	if err := storage.SetSetting(db, KeyURL, errSrv.URL); err != nil {
		t.Fatal(err)
	}
	if err := FromDB(db).Send(Event{Event: "x"}); err == nil {
		t.Fatal("500 response should error")
	}
}
