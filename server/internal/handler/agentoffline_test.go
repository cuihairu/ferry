package handler

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAgentOfflineAlert covers F-1821: an agent disconnect lands one
// agent_offline warning into the herald outbox for the node; a maintenance
// node stays quiet (the operator took it down on purpose).
func TestAgentOfflineAlert(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "aoff-1")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "aoff-1", token)
	c.Close()

	// The read loop ends asynchronously: poll for the outbox row.
	var ev storage.Event
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := db.Where("kind = ?", herald.KindAgentOffline).First(&ev).Error
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent_offline event missing: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ev.Target != herald.TargetAdmin || ev.Severity != herald.SeverityWarning {
		t.Fatalf("event shape: %+v", ev)
	}
	if ev.DedupKey != fmt.Sprintf("node:%d:agent_offline", id) {
		t.Fatalf("dedup key = %q", ev.DedupKey)
	}
	if ev.Status != herald.StatusPending {
		t.Fatalf("outbox status = %q, want pending", ev.Status)
	}

	// A maintenance node disconnecting must not alert.
	if err := db.Model(&storage.Node{}).Where("id = ?", id).
		Update("maintenance", true).Error; err != nil {
		t.Fatal(err)
	}
	c2 := fakeAgent(t, srv.URL, "aoff-1", token)
	c2.Close()
	time.Sleep(300 * time.Millisecond)
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindAgentOffline).
		Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("maintenance node alerted: %d events", n)
	}
}
