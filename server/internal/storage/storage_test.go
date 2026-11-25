package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// openTest opens the database under test. It prefers FERRY_TEST_PG_DSN and falls back to SQLite when unset.
func openTest(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("FERRY_TEST_PG_DSN")
	if dsn != "" {
		db, err := Open(DriverPostgres, dsn)
		if err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		// The PG test DB is reused across runs, so clear its data first (children before parents to avoid foreign keys); SQLite uses a temp directory each time and needs no cleanup.
		purgeTables(t, db)
		return db
	}
	db, err := Open(DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// purgeTables clears all business tables in dependency order.
func purgeTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, tb := range []string{
		"traffic_logs", "probe_reports", "landing_assignments", "dimension_statuses",
		"node_configs", "grants", "payment_transactions", "payment_orders",
		"card_codes", "card_batches", "nodes", "users",
	} {
		if err := db.Exec("DELETE FROM " + tb).Error; err != nil {
			t.Fatalf("purge %s: %v", tb, err)
		}
	}
}

func TestSQLiteCRUD(t *testing.T) {
	runCRUD(t, openTest(t))
}

// runCRUD is the CRUD + aggregation acceptance shared by the three dialects (reused by the PG dialect tests).
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

	// nodes (unique token)
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

	// traffic_logs aggregation (minimal acceptance of the accounting policy)
	now := time.Now()
	nodeID := n.ID
	uid := u.ID
	for i := 0; i < 3; i++ {
		tl := TrafficLog{UserID: &uid, NodeID: &nodeID, RxBytes: 10, TxBytes: 5, RecordedAt: now}
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

	// The card-code tables exist and are writable (a prerequisite for the first payment segment)
	batch := CardBatch{Name: "b1", GrantType: "add_quota", GrantValue: 1 << 30, Total: 2, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	code := CardCode{BatchID: batch.ID, Code: "ABCD1234", Status: "unused"}
	if err := db.Create(&code).Error; err != nil {
		t.Fatalf("create code: %v", err)
	}

	// One of the three ledgers, the orders table, is writable
	ord := PaymentOrder{OrderNo: "F20251005ABC", UserID: u.ID, Provider: "card", AmountCents: 1000, Product: "10GB", Status: "pending"}
	if err := db.Create(&ord).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	// Deleting a user does not cascade-clear billing history (P2 fix): the traffic rows are kept with user_id nulled.
	if err := db.Delete(&User{}, u.ID).Error; err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var rest int64
	db.Model(&TrafficLog{}).Count(&rest)
	if rest != 3 {
		t.Fatalf("traffic logs should survive user delete, remain %d", rest)
	}
	var orphan int64
	db.Model(&TrafficLog{}).Where("user_id IS NOT NULL").Count(&orphan)
	if orphan != 0 {
		t.Fatalf("detached traffic logs should have null user_id, got %d", orphan)
	}
}

func TestLandingAssignmentAndProbeReport(t *testing.T) {
	db := openTest(t)

	entry := Node{Name: "entry-1", Address: "sh.example.com", Port: 443, Protocol: "vless", Config: "{}", Enabled: true, Token: "tok-e1"}
	landing := Node{Name: "landing-1", Address: "sg.example.com", Port: 443, Protocol: "vless", Config: "{}", Enabled: true, Token: "tok-l1"}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatalf("create entry node: %v", err)
	}
	if err := db.Create(&landing).Error; err != nil {
		t.Fatalf("create landing node: %v", err)
	}

	// Landing assignment rows: an effective one is queryable
	la := LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landing.ID,
		Direction: "out", Strategy: "manual", Weight: 10,
		Reason: "manual", AssignedAt: time.Now(),
	}
	if err := db.Create(&la).Error; err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	var active []LandingAssignment
	if err := db.Where("released_at IS NULL").Find(&active).Error; err != nil {
		t.Fatalf("query active assignments: %v", err)
	}
	if len(active) != 1 || active[0].LandingNodeID != landing.ID || active[0].EntryNodeID == nil {
		t.Fatalf("active assignments: %+v", active)
	}

	// Probe conclusions are stored: queryable by prober and target kind
	target := landing.ID
	pr := ProbeReport{
		NodeID: entry.ID, TargetKind: "tunnel", TargetNodeID: &target,
		Direction: "out", RttMs: 48, LossPct: 0,
		Reachable: true, Verdict: "healthy",
		Region: "华东", ISP: "电信", ProbedAt: time.Now(),
	}
	if err := db.Create(&pr).Error; err != nil {
		t.Fatalf("create probe report: %v", err)
	}
	var reports []ProbeReport
	if err := db.Where("node_id=? AND target_kind=?", entry.ID, "tunnel").Order("id").Find(&reports).Error; err != nil {
		t.Fatalf("query probe reports: %v", err)
	}
	if len(reports) != 1 || reports[0].RttMs != 48 || reports[0].Region != "华东" ||
		reports[0].ISP != "电信" || reports[0].TargetNodeID == nil {
		t.Fatalf("probe reports: %+v", reports)
	}
}

func TestCardBatchCascade(t *testing.T) {
	db := openTest(t)
	batch := CardBatch{Name: "测试批次", GrantType: "add_quota", GrantValue: 1 << 30, Total: 2, CreatedBy: "admin"}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatalf("create batch: %v", err)
	}
	codes := []CardCode{{BatchID: batch.ID, Code: "AAAA-BBBB-CCCC"}, {BatchID: batch.ID, Code: "DDDD-EEEE-FFFF"}}
	if err := db.Create(&codes).Error; err != nil {
		t.Fatalf("create codes: %v", err)
	}
	// Deleting a batch cascades to its card codes (DB-level FK CASCADE)
	if err := db.Delete(&batch).Error; err != nil {
		t.Fatalf("delete batch: %v", err)
	}
	var cnt int64
	if err := db.Model(&CardCode{}).Where("batch_id=?", batch.ID).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("card codes should cascade, got %d", cnt)
	}
}

// TestSettings covers GetSetting/SetSetting: write then read back, ok=false for a missing key, and
// overwriting the same key.
func TestSettings(t *testing.T) {
	db := openTest(t)

	if _, ok, err := GetSetting(db, "missing-key"); err != nil || ok {
		t.Fatalf("GetSetting missing = %q, %v, %v; want \"\", false, nil", "", ok, err)
	}
	if err := SetSetting(db, "wm", "42"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	v, ok, err := GetSetting(db, "wm")
	if err != nil || !ok || v != "42" {
		t.Fatalf("GetSetting = %q, %v, %v; want 42, true, nil", v, ok, err)
	}
	if err := SetSetting(db, "wm", "100"); err != nil {
		t.Fatalf("SetSetting update: %v", err)
	}
	v, ok, err = GetSetting(db, "wm")
	if err != nil || !ok || v != "100" {
		t.Fatalf("GetSetting after update = %q, %v, %v; want 100, true, nil", v, ok, err)
	}
}

// TestOpenDriverGuards covers Open's dialect dispatch: an empty driver falls back to sqlite, postgres/mysql
// with an empty connection string are refused, and an unknown driver is refused.
func TestOpenDriverGuards(t *testing.T) {
	db, err := Open("", ":memory:")
	if err != nil || db == nil {
		t.Fatalf("empty driver should fall back sqlite: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
	for driver, dsn := range map[string]string{
		DriverPostgres: "", DriverMySQL: "", "oracle": "x",
	} {
		if _, err := Open(driver, dsn); err == nil {
			t.Fatalf("open %s with %q should error", driver, dsn)
		}
	}
}

// TestSQLiteDSN covers connection-string completion: the default file name, file: prefixes and ?/# segment truncation, nested
// directories being created, an error when the directory is a file, and an in-memory DB passing through as is.
func TestSQLiteDSN(t *testing.T) {
	if got, err := sqliteDSN(""); err != nil || !strings.Contains(got, "ferry.db?") {
		t.Fatalf("default dsn = %q err=%v", got, err)
	}
	if got, err := sqliteDSN(":memory:"); err != nil || got != ":memory:" {
		t.Fatalf("memory passthrough = %q err=%v", got, err)
	}
	if got, err := sqliteDSN("file:/tmp/x.db?_fk=1#frag"); err != nil ||
		!strings.Contains(got, "file:/tmp/x.db?_fk=1#frag&_pragma=journal_mode(WAL)") {
		t.Fatalf("file prefix strip = %q err=%v", got, err)
	}
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "ferry.db")
	got, err := sqliteDSN(nested)
	if err != nil || !strings.Contains(got, nested+"?") || !strings.Contains(got, "_pragma=journal_mode(WAL)") {
		t.Fatalf("nested dsn = %q err=%v", got, err)
	}
	if st, err := os.Stat(filepath.Join(base, "a")); err != nil || !st.IsDir() {
		t.Fatal("nested dir not created")
	}
	// The directory path is occupied by a file: creating the DB directory fails.
	blocker := filepath.Join(t.TempDir(), "file.db")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sqliteDSN(filepath.Join(blocker, "inner.db")); err == nil {
		t.Fatal("dir blocked by file should error")
	}
}
