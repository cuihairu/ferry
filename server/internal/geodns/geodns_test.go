package geodns

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// fakeChan 记录每次 upsert/delete（name|value 与 name），可注入失败。
type fakeChan struct {
	upserts []string
	deletes []string
	err     error
}

func (f *fakeChan) Kind() string { return "fake" }
func (f *fakeChan) Upsert(_ context.Context, name, rtype, value string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, name+"|"+rtype+"|"+value)
	return nil
}
func (f *fakeChan) Delete(_ context.Context, name, rtype string) error {
	if f.err != nil {
		return f.err
	}
	f.deletes = append(f.deletes, name+"|"+rtype)
	return nil
}

// newTestDB 建内存库（storage.Open + AutoMigrate，口径同 recovery 测试）。
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

// newSyncer 建内存库 Syncer（带假通道注入）：启用 DNS 商 + 前置域名。
func newSyncer(t *testing.T, fake *fakeChan) (*Syncer, *fakeChan) {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("geo-master")
	sealed, err := store.Encrypt("tok-geo")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.9.9.1", BackupIPs: `["10.9.9.2"]`}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	s := &Syncer{DB: db, Store: store, New: func(kind, token string) (dns.Provider, error) {
		if kind != "cloudflare" || token != "tok-geo" {
			t.Fatalf("factory got kind=%q token=%q", kind, token)
		}
		return fake, nil
	}}
	return s, fake
}

// addNode 入库一个在池入口节点；region 空=未知区。
func addNode(t *testing.T, s *Syncer, name, region string, rttMs int) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Region: region, Port: 443, Protocol: "vless",
		Enabled: true, Token: "t-" + name, Status: "online",
		Role: "entry", PoolState: "active", Address: ipOf(name)}
	if err := s.DB.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	if rttMs > 0 {
		pr := storage.ProbeReport{NodeID: n.ID, TargetKind: "tunnel", Reachable: true,
			RttMs: rttMs, ProbedAt: time.Now()}
		if err := s.DB.Create(&pr).Error; err != nil {
			t.Fatal(err)
		}
	}
	return n
}

func ipOf(name string) string { return "10.1.0." + name[len(name)-1:] }

// records 取已同步面全部行。
func records(t *testing.T, s *Syncer) []storage.GeoDNSRecord {
	t.Helper()
	rows := []storage.GeoDNSRecord{}
	if err := s.DB.Order("slug").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestSlug 区域词表归一：大小写/下划线/空白/非法字符，纯中文归一为空。
func TestSlug(t *testing.T) {
	cases := map[string]string{
		"US-East":    "us-east",
		"  Asia JP ": "asia-jp",
		"hk_01":      "hk-01",
		"欧 洲":        "",
		"--de--":     "de",
		"SG@a b":     "sga-b", // 非法字符剔除不补分隔，仅保证字符集合法
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegionReps 每区域 RTT 升序取首位、无数据殿后；不在池/非入口不出参。
func TestRegionReps(t *testing.T) {
	s, _ := newSyncer(t, &fakeChan{})
	slow := addNode(t, s, "us-slow", "US", 300)
	fast := addNode(t, s, "us-fast", "US", 80)
	nodata := addNode(t, s, "us-nodata", "US", 0)
	addNode(t, s, "jp-entry", "JP", 50)
	// 摘除态/停用节点不出参。
	addNode(t, s, "hk-out", "HK", 10)
	if err := s.DB.Model(&storage.Node{}).Where("name = ?", "hk-out").
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	addNode(t, s, "sg-off", "SG", 10)
	if err := s.DB.Model(&storage.Node{}).Where("name = ?", "sg-off").
		UpdateColumn("enabled", false).Error; err != nil { // 停用节点不出参
		t.Fatal(err)
	}

	reps, err := s.RegionReps(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("regions = %d (%v), want 2", len(reps), reps)
	}
	if reps["US"].ID != fast.ID {
		t.Errorf("US rep = %s (want us-fast, nodata id=%d slow id=%d)", reps["US"].Name, nodata.ID, slow.ID)
	}
	if reps["JP"].Name != "jp-entry" {
		t.Errorf("JP rep = %s", reps["JP"].Name)
	}
}

// TestSyncNewThenIdempotent 首轮落新记录，二轮幂等零变更。
func TestSyncNewThenIdempotent(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 200)

	n, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("changed = %d, want 2", n)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
	rows := records(t, s)
	if len(rows) != 2 || rows[0].Name != "jp.edge.example.com" || rows[0].Value != ipOf("jp-1") {
		t.Fatalf("records = %+v", rows)
	}

	// 二轮：无变化零 upsert。
	n, err = s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sync = %d, %v; want 0, nil", n, err)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts after 2nd = %v", fake.upserts)
	}
}

// TestSyncRttFlipRewrites RTT 翻转代表易主：Upsert 同一行（ID 不变）。
func TestSyncRttFlipRewrites(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	a := addNode(t, s, "us-a", "US", 100)
	addNode(t, s, "us-b", "US", 200)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := records(t, s)
	if len(rows) != 1 || rows[0].Value != ipOf("us-a") {
		t.Fatalf("records = %+v", rows)
	}
	firstID := rows[0].ID

	// us-b 变快：代表换人，原行重写。
	if err := s.DB.Model(&storage.ProbeReport{}).Where("node_id = ?", a.ID).
		Update("rtt_ms", 500.0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows = records(t, s)
	if len(rows) != 1 || rows[0].Value != ipOf("us-b") || rows[0].ID != firstID {
		t.Fatalf("after flip records = %+v (first id=%d)", rows, firstID)
	}
	if len(fake.upserts) != 2 || fake.upserts[1] != "us.edge.example.com|A|"+ipOf("us-b") {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestSyncRegionGoneDeletes 区域全挂撤记录：通道 Delete + 本地行删除。
func TestSyncRegionGoneDeletes(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	a := addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 50)
	if _, err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(records(t, s)) != 2 {
		t.Fatal("expected 2 records")
	}

	// US 区域唯一入口摘除：该区域 slug 记录撤销。
	if err := s.DB.Model(&storage.Node{}).Where("id = ?", a.ID).
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	n, err := s.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(fake.deletes) != 1 || fake.deletes[0] != "us.edge.example.com|A" {
		t.Fatalf("changed=%d deletes=%v", n, fake.deletes)
	}
	rows := records(t, s)
	if len(rows) != 1 || rows[0].Slug != "jp" {
		t.Fatalf("records = %+v", rows)
	}
}

// TestSyncChineseRegionSkipped 纯中文区域名归一为空不产记录。
func TestSyncChineseRegionSkipped(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "cn-1", "华东", 100)
	n, err := s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("changed=%d err=%v; want 0 no-op", n, err)
	}
	if len(fake.upserts) != 0 || len(records(t, s)) != 0 {
		t.Fatal("chinese region must not produce records")
	}
}

// TestSyncNoProvider 未配置启用 DNS 商：no-op（功能未启用）。
func TestSyncNoProvider(t *testing.T) {
	fake := &fakeChan{}
	s, _ := newSyncer(t, fake)
	if err := s.DB.Where("1=1").Delete(&storage.DNSProvider{}).Error; err != nil {
		t.Fatal(err)
	}
	addNode(t, s, "us-1", "US", 100)
	n, err := s.Sync(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("changed=%d err=%v; want 0 no-op", n, err)
	}
}

// TestSyncFailureAlerts 通道失败：回错误且按日 dedup 只发一条告警；
// 其余记录继续处理（部分失败不整体中断）。
func TestSyncFailureAlerts(t *testing.T) {
	fake := &fakeChan{}
	s, fake := newSyncer(t, fake)
	addNode(t, s, "us-1", "US", 100)
	addNode(t, s, "jp-1", "JP", 50)
	fake.err = errors.New("api down")

	n, err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("channel failure must surface")
	}
	if n != 0 {
		t.Fatalf("changed = %d, want 0", n)
	}
	var alerts int64
	if err := s.DB.Model(&storage.Event{}).Where("kind = ?", "geodns_sync_failed").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("alerts = %d, want 1 (dedup per day)", alerts)
	}

	// 同日重试再失败：告警不重复。
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("must still fail")
	}
	if err := s.DB.Model(&storage.Event{}).Where("kind = ?", "geodns_sync_failed").Count(&alerts).Error; err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("alerts after retry = %d, want 1", alerts)
	}

	// 恢复后成功落记录。
	fake.err = nil
	if n, err := s.Sync(context.Background()); err != nil || n != 2 {
		t.Fatalf("recovered sync = %d, %v", n, err)
	}
}
