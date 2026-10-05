package storage

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

// openTest 打开临时 SQLite 库（含全部 AutoMigrate）。
func openTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestSQLiteCRUD(t *testing.T) {
	runCRUD(t, openTest(t))
}

// runCRUD 是三方言共用的 CRUD + 聚合验收（PG 方言测试复用）。
func runCRUD(t *testing.T, db *gorm.DB) {
	t.Helper()

	// users
	u := User{Username: "alice", SubToken: "tok-alice", QuotaBytes: 100, Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("user id not assigned")
	}
	var got User
	if err := db.First(&got, u.ID).Error; err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Username != "alice" || got.QuotaBytes != 100 {
		t.Fatalf("user mismatch: %+v", got)
	}
	if err := db.Model(&User{}).Where("id=?", u.ID).Update("quota_bytes", 200).Error; err != nil {
		t.Fatalf("update user: %v", err)
	}
	db.First(&got, u.ID)
	if got.QuotaBytes != 200 {
		t.Fatalf("update lost, quota=%d", got.QuotaBytes)
	}

	// nodes（令牌唯一）
	n := Node{Name: "hk-1", Address: "hk.example.com", Port: 443, Protocol: "vless", Config: "{}", Enabled: true, Token: "node-tok-1", Status: "unknown"}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	dup := n
	dup.ID = 0
	dup.Name = "hk-2"
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("duplicate node token must fail")
	}
	var nodes []Node
	if err := db.Order("id").Find(&nodes).Error; err != nil || len(nodes) != 1 {
		t.Fatalf("list nodes: %v len=%d", err, len(nodes))
	}

	// traffic_logs 聚合（统计口径的最小验收）
	now := time.Now()
	nodeID := n.ID
	for i := 0; i < 3; i++ {
		tl := TrafficLog{UserID: u.ID, NodeID: &nodeID, RxBytes: 10, TxBytes: 5, RecordedAt: now}
		if err := db.Create(&tl).Error; err != nil {
			t.Fatalf("create traffic log: %v", err)
		}
	}
	var total int64
	if err := db.Raw(`SELECT COALESCE(SUM(rx_bytes),0)+COALESCE(SUM(tx_bytes),0) FROM traffic_logs WHERE user_id=?`, u.ID).Scan(&total).Error; err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if total != 45 {
		t.Fatalf("aggregate = %d, want 45", total)
	}

	// 卡密表存在且可写（支付一段的前置）
	batch := CardBatch{Name: "b1", GrantType: "add_quota", GrantValue: 1 << 30, Total: 2, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	code := CardCode{BatchID: batch.ID, Code: "ABCD1234", Status: "unused"}
	if err := db.Create(&code).Error; err != nil {
		t.Fatalf("create code: %v", err)
	}

	// 三账之一的订单表可写
	ord := PaymentOrder{OrderNo: "F20251005ABC", UserID: u.ID, Provider: "card", AmountCents: 1000, Product: "10GB", Status: "pending"}
	if err := db.Create(&ord).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	// 删除用户级联（traffic 记录随用户删除）
	if err := db.Delete(&User{}, u.ID).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var rest int64
	db.Model(&TrafficLog{}).Count(&rest)
	if rest != 0 {
		t.Fatalf("traffic logs should cascade, remain %d", rest)
	}
}
