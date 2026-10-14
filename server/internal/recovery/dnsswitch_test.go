package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// fakeDNS 记录每次 upsert（name|type|value），可注入失败。
type fakeDNS struct {
	upserts []string
	err     error
}

func (f *fakeDNS) Kind() string { return "fake" }
func (f *fakeDNS) Upsert(ctx context.Context, name, rtype, value string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, name+"|"+rtype+"|"+value)
	return nil
}
func (f *fakeDNS) Delete(ctx context.Context, name, rtype string) error {
	if f.err != nil {
		return f.err
	}
	f.upserts = append(f.upserts, "del:"+name+"|"+rtype)
	return nil
}

// setupDNSAction 入库一条启用 DNS 商与一条前置记录，返回注入 fake 的动作。
func setupDNSAction(t *testing.T, backups string, fake *fakeDNS) *DNSAction {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("dns-master")
	sealed, err := store.Encrypt("tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	front := storage.DNSFront{Name: "主入口", Domain: "edge.example.com", ProviderID: prov.ID,
		PrimaryIP: "10.0.0.1", BackupIPs: backups}
	if err := db.Create(&front).Error; err != nil {
		t.Fatal(err)
	}
	// 被封入口节点（OnDone 链路测试要查它复位）。
	node := storage.Node{Name: "entry-1", Port: 443, Protocol: "vless", Enabled: true,
		Token: "t-entry-1", Status: "online", PoolState: "suspended"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	return &DNSAction{
		DB: db, Store: store,
		New: func(kind, token string) (dns.Provider, error) {
			if kind != "cloudflare" || token != "tok-secret" {
				t.Fatalf("factory got kind=%q token=%q", kind, token)
			}
			return fake, nil
		},
	}
}

// loadFront 取唯一前置记录。
func loadFront(t *testing.T, a *DNSAction) storage.DNSFront {
	t.Helper()
	var f storage.DNSFront
	if err := a.DB.First(&f).Error; err != nil {
		t.Fatal(err)
	}
	return f
}

// TestDNSActionSwitch L1 切换：备用 IP 按轮换游标取用，upsert 成功才落
// switched；再次切离轮换到下一个备用 IP。
func TestDNSActionSwitch(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2","10.0.0.3"]`, fake)
	node := storage.Node{Name: "entry-1"}

	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	f := loadFront(t, a)
	if !f.Switched || f.CurrentIP != "10.0.0.2" || f.SwitchIndex != 1 {
		t.Fatalf("after switch = %+v", f)
	}
	if len(fake.upserts) != 1 || fake.upserts[0] != "edge.example.com|A|10.0.0.2" {
		t.Fatalf("upserts = %v", fake.upserts)
	}

	// 模拟又一轮判封（手动复位切离态）：轮换到下一个备用 IP。
	if err := a.DB.Model(&storage.DNSFront{}).Where("id = ?", f.ID).
		Update("switched", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	f = loadFront(t, a)
	if f.CurrentIP != "10.0.0.3" || f.SwitchIndex != 2 {
		t.Fatalf("after rotate = %+v", f)
	}
}

// TestDNSActionRestore 回切：切离记录 Upsert 回 PrimaryIP 并清 switched；
// 未配置前置时失败留原因（编排按失败推进 L2）。
func TestDNSActionRestore(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	node := storage.Node{Name: "entry-1"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := loadFront(t, a)
	if f.Switched || f.CurrentIP != f.PrimaryIP {
		t.Fatalf("after restore = %+v", f)
	}
	if got := fake.upserts[len(fake.upserts)-1]; got != "edge.example.com|A|10.0.0.1" {
		t.Fatalf("last upsert = %q", got)
	}

	// 无切离记录时回切是空操作。
	if err := a.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.upserts) != 2 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestDNSActionNotConfigured 未配置域名前置：失败留原因，不冒称成功。
func TestDNSActionNotConfigured(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.DB.Delete(&storage.DNSFront{}, 1).Error; err != nil {
		t.Fatal(err)
	}
	err := a.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil {
		t.Fatal("unconfigured L1 must fail")
	}
	if len(fake.upserts) != 0 {
		t.Fatalf("upserts = %v", fake.upserts)
	}
}

// TestDNSActionUpsertFailed DNS 商报错：Run 失败推进（不落 switched 半态）。
func TestDNSActionUpsertFailed(t *testing.T) {
	fake := &fakeDNS{err: errors.New("api down")}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err == nil {
		t.Fatal("upsert failure must propagate")
	}
	f := loadFront(t, a)
	if f.Switched {
		t.Fatalf("half state leaked: %+v", f)
	}
}

// TestOnDoneRestoresDNS 恢复收尾钩子：探测恢复（done）经 OnDone 回切
// 前置记录常态——接通「复位 → 回切」闭环。
func TestOnDoneRestoresDNS(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	node := storage.Node{Name: "entry-1"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatal(err)
	}

	// 开一条进行中的恢复流水线，节点已复位（pool active）。
	db := a.DB
	var n storage.Node
	if err := db.Where("name = ?", "entry-1").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
		Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 1, State: StateRunning,
		Action: "dns_switch", ActionState: ActionRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}

	var hookCalls int
	opts := Options{OnDone: func(nodeID uint, nodeName string) {
		hookCalls++
		if err := a.Restore(context.Background()); err != nil {
			t.Errorf("restore: %v", err)
		}
	}}
	if err := Sweep(db, now, opts, Registry{1: a}, nil); err != nil {
		t.Fatal(err)
	}
	if hookCalls != 1 {
		t.Fatalf("hookCalls = %d", hookCalls)
	}
	f := loadFront(t, a)
	if f.Switched || f.CurrentIP != f.PrimaryIP {
		t.Fatalf("not restored: %+v", f)
	}
	var got storage.Recovery
	if err := db.First(&got, rec.ID).Error; err != nil || got.State != StateDone {
		t.Fatalf("recovery = %+v err=%v", got, err)
	}
}

// TestParseBackups 备用 IP 解析口径：空串/全空项为空集，非字符串数组
// 报 JSON 错（错误文案带配置字段名便于排障）。
func TestParseBackups(t *testing.T) {
	ips, err := parseBackups("")
	if err != nil || len(ips) != 0 {
		t.Fatalf("empty raw = %v err=%v", ips, err)
	}
	ips, err = parseBackups(`["",""]`)
	if err != nil || len(ips) != 0 {
		t.Fatalf("all blanks = %v err=%v", ips, err)
	}
	ips, err = parseBackups(`["10.0.0.2",""]`)
	if err != nil || len(ips) != 1 || ips[0] != "10.0.0.2" {
		t.Fatalf("filtered = %v err=%v", ips, err)
	}
	if _, err := parseBackups("not json"); err == nil ||
		!strings.Contains(err.Error(), "JSON string array") {
		t.Fatalf("bad json err = %v", err)
	}
	if _, err := parseBackups(`[1,2]`); err == nil ||
		!strings.Contains(err.Error(), "JSON string array") {
		t.Fatalf("number array err = %v", err)
	}
}

// TestDNSActionFactoryDefault 未注入工厂时缺省走 dns.New 插件位。
func TestDNSActionFactoryDefault(t *testing.T) {
	if a := (&DNSAction{}); a.factory() == nil {
		t.Fatal("default factory must resolve to dns.New")
	}
}

// TestDNSActionNoMasterKey 未配置主密钥：Restore 阶段解不出凭证即失败，
// 不冒称回切成功。
func TestDNSActionNoMasterKey(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	// 另一实例只带库不带密钥库：切离记录回切时在 providerFor 处失败。
	b := &DNSAction{DB: a.DB}
	err := b.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "master key not configured") {
		t.Fatalf("err = %v; want master key not configured", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("front must stay switched on failed restore: %+v", f)
	}
}

// TestDNSActionRestoreUpsertFailed 回切时 DNS 商报错：失败冒出，切离态
// 不被清（下轮收尾再试）。
func TestDNSActionRestoreUpsertFailed(t *testing.T) {
	fake := &fakeDNS{}
	a := setupDNSAction(t, `["10.0.0.2"]`, fake)
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	fake.err = errors.New("api down")
	err := a.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recovery: dns restore edge.example.com: api down") {
		t.Fatalf("err = %v; want restore upsert failure", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("front cleared on failed restore: %+v", f)
	}
}

// failUpdates 注册一个必失败的更新回调（gorm 注错位），模拟落库失败。
func failUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Callback().Update().Before("gorm:update").Register("test:fail_update",
		func(tx *gorm.DB) { tx.AddError(errors.New("db down")) }); err != nil {
		t.Fatal(err)
	}
}

// TestDNSActionSwitchDBUpdateFailed upsert 成功但落 switched 失败：错误
// 冒出且不留半态（switched 不置位，下轮重切）。
func TestDNSActionSwitchDBUpdateFailed(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	failUpdates(t, a.DB)
	err := a.Run(context.Background(), storage.Node{Name: "entry-1"})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v; want db failure", err)
	}
	if f := loadFront(t, a); f.Switched || f.CurrentIP != "" {
		t.Fatalf("half state leaked: %+v", f)
	}
}

// TestDNSActionRestoreDBUpdateFailed 回切 upsert 成功但清 switched 落库
// 失败：错误冒出，记录保持切离（幂等重试安全）。
func TestDNSActionRestoreDBUpdateFailed(t *testing.T) {
	a := setupDNSAction(t, `["10.0.0.2"]`, &fakeDNS{})
	if err := a.Run(context.Background(), storage.Node{Name: "entry-1"}); err != nil {
		t.Fatal(err)
	}
	failUpdates(t, a.DB)
	err := a.Restore(context.Background())
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("err = %v; want db failure", err)
	}
	if f := loadFront(t, a); !f.Switched {
		t.Fatalf("switched cleared despite db failure: %+v", f)
	}
}
