package recovery

import (
	"context"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestStandbyActionPromote covers the L2 standby backfill: take an online standby
// entry from the list and promote it into the pool with a trace; an empty list fails with a reason and advances to L3.
func TestStandbyActionPromote(t *testing.T) {
	db := newTestDB(t)
	act := &StandbyAction{DB: db}

	// No standby node: fails with a reason (never faking success).
	err := act.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil || !strings.Contains(err.Error(), "no standby") {
		t.Fatalf("empty standby err = %v", err)
	}

	// Offline standby nodes are not taken; an online standby node is promoted into the pool.
	mk := func(name, status string) storage.Node {
		n := storage.Node{Name: name, Port: 443, Protocol: "vless", Enabled: true,
			Token: "tok-" + name, Status: status, Role: "entry"}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
			Update("pool_state", pool.StateStandby).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	mk("sb-off", "offline")
	sb := mk("sb-on", "online")

	if err := act.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	var got storage.Node
	if err := db.First(&got, sb.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.PoolState != pool.StateActive || !strings.Contains(got.PoolReason, "恢复补位") {
		t.Fatalf("after promote = %+v", got)
	}
}
