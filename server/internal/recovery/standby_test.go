package recovery

import (
	"context"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestStandbyActionPromote 覆盖 L2 预备机补位：从清单取在线备用入口升池
// 留痕；清单空失败留原因推进 L3。
func TestStandbyActionPromote(t *testing.T) {
	db := newTestDB(t)
	act := &StandbyAction{DB: db}

	// 无预备机：失败留原因（不冒称成功）。
	err := act.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil || !strings.Contains(err.Error(), "no standby") {
		t.Fatalf("empty standby err = %v", err)
	}

	// 离线预备机不取用；在线预备机升池。
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
