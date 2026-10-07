package relaypush

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestRenderOverride 覆盖落地覆盖 spec 渲染：地址=address:port，
// ws-tls 透传插件名，其余走缺省（字段省略），空地址拒绝。
func TestRenderOverride(t *testing.T) {
	ov, err := RenderOverride(storage.Node{Address: "hk.example.com", Port: 443, Transport: "tls"})
	if err != nil {
		t.Fatal(err)
	}
	if ov != `{"landing_addr":"hk.example.com:443"}` {
		t.Fatalf("tls override = %s", ov)
	}
	ov, err = RenderOverride(storage.Node{Address: "10.0.0.2", Port: 8443, Transport: "ws-tls"})
	if err != nil {
		t.Fatal(err)
	}
	if ov != `{"landing_addr":"10.0.0.2:8443","tunnel":"ws-tls"}` {
		t.Fatalf("ws-tls override = %s", ov)
	}
	if _, err := RenderOverride(storage.Node{Port: 443}); err == nil {
		t.Fatal("empty address must fail")
	}
}

// TestRepoint 覆盖重指下发：offline 也留痕 failed（重试依据）；
// manual 优先于 auto；无生效分配不动作。
func TestRepoint(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil) // 空 hub：全部离线

	entry := storage.Node{Name: "entry-x", Token: "t1", Role: "entry", Enabled: true}
	landA := storage.Node{Name: "land-a", Token: "t2", Role: "landing", Enabled: true,
		Address: "a.example.com", Port: 443, Transport: "tls"}
	landB := storage.Node{Name: "land-b", Token: "t3", Role: "landing", Enabled: true,
		Address: "b.example.com", Port: 8443, Transport: "ws-tls"}
	for _, n := range []*storage.Node{&entry, &landA, &landB} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 无分配：不动作。
	if err := p.Repoint(entry.ID); err != nil {
		t.Fatalf("repoint without assignment: %v", err)
	}
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 0 {
		t.Fatalf("node configs without assignment = %d", n)
	}

	seedAssign := func(landing uint, strategy string) {
		t.Helper()
		if err := db.Create(&storage.LandingAssignment{
			EntryNodeID: &entry.ID, LandingNodeID: landing, Strategy: strategy,
			Direction: "out", AssignedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// auto 指向 land-a：offline 留痕 failed，payload 为渲染出的覆盖 spec。
	seedAssign(landA.ID, alloc.PolicyCostFirst)
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	var row storage.NodeConfig
	if err := db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Proc != "relay" || row.Kind != "relay" || row.Status != "failed" {
		t.Fatalf("config row = %+v", row)
	}
	if row.Payload != `{"landing_addr":"a.example.com:443"}` {
		t.Fatalf("payload = %s", row.Payload)
	}

	// 再挂一条更新的 auto 行指向 land-b：取最新 auto。
	seedAssign(landB.ID, alloc.PolicyLeastConn)
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	row = storage.NodeConfig{} // 复用结构体带主键会变查询条件，先清零
	db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row)
	if row.Payload != `{"landing_addr":"b.example.com:8443","tunnel":"ws-tls"}` {
		t.Fatalf("payload (latest auto) = %s", row.Payload)
	}

	// 更早的 manual 行优先于一切 auto：manual 指回 land-a。
	manual := storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landA.ID, Strategy: alloc.PolicyManual,
		Direction: "out", AssignedAt: time.Now().Add(-time.Hour),
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	row = storage.NodeConfig{}
	db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row)
	if row.Payload != `{"landing_addr":"a.example.com:443"}` {
		t.Fatalf("payload (manual wins) = %s", row.Payload)
	}
}

// TestOnSwitchDedup 覆盖换线回调：同一入口多事件只推一次。
func TestOnSwitchDedup(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil)

	entry := storage.Node{Name: "entry-x", Token: "t1", Role: "entry", Enabled: true}
	land := storage.Node{Name: "land-x", Token: "t2", Role: "landing", Enabled: true,
		Address: "x.example.com", Port: 443, Transport: "tls"}
	for _, n := range []*storage.Node{&entry, &land} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: land.ID, Strategy: alloc.PolicyCostFirst,
		Direction: "out", AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	p.OnSwitch([]alloc.SwitchEvent{
		{EntryID: entry.ID, EntryName: entry.Name, ToName: "land-b"},
		{EntryID: entry.ID, EntryName: entry.Name, ToName: "land-c"},
	})
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 1 {
		t.Fatalf("node configs after dedup = %d", n)
	}
}
