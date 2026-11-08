package recovery

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAdvanceUnknownActionStateFallsThrough 动作态是未认识的值：不推进不
// 启动，静默等人工（防御脏数据）。
func TestAdvanceUnknownActionStateFallsThrough(t *testing.T) {
	db := newTestDB(t)
	changed := time.Now().Add(-DefaultSustainedAfter - time.Minute)
	n := suspendedNode(t, db, "weird-1", "auto", changed)
	if err := Sweep(db, time.Now(), Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	// 开线后把动作态改成脏值，再 Sweep 一轮：不动状态机。
	rec := loadRecovery(t, db, n.ID)
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
		Update("action_state", "weird").Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, time.Now().Add(3*time.Hour), Options{}, Registry{}, nil); err != nil {
		t.Fatalf("sweep with unknown action_state: %v", err)
	}
	after := loadRecovery(t, db, n.ID)
	if after.ActionState != "weird" || after.Level != rec.Level {
		t.Fatalf("row = %+v; unknown action_state must not advance", after)
	}
}

// TestDNSActionSwitchableNoProviders 未配 DNS 商：切换报未配置、回切空操作。
func TestDNSActionSwitchableNoProviders(t *testing.T) {
	a := &DNSAction{DB: newTestDB(t)}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "dns front not configured") {
		t.Fatalf("run err = %v", err)
	}
	if err := a.Restore(context.Background()); err != nil {
		t.Fatalf("restore with nothing switched = %v; want nil", err)
	}
}

// TestDNSActionBadBackupIPs 备用 IP 列表不是 JSON 数组：切换报错留域名。
func TestDNSActionBadBackupIPs(t *testing.T) {
	a := setupDNSAction(t, "{oops", &fakeDNS{})
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "dns front edge.example.com") ||
		!strings.Contains(err.Error(), "backup_ips") {
		t.Fatalf("err = %v; want backup_ips parse error", err)
	}
}

// TestDNSActionNoBackupIPs 备用 IP 全空：切换报无可用备用。
func TestDNSActionNoBackupIPs(t *testing.T) {
	a := setupDNSAction(t, `["", ""]`, &fakeDNS{})
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "no backup ips") {
		t.Fatalf("err = %v", err)
	}
}

// TestDNSActionNoMasterKeyOnSwitch 凭证入口未配：切换在解密边界即报错。
func TestDNSActionNoMasterKeyOnSwitch(t *testing.T) {
	a := setupDNSAction(t, `["1.1.1.1"]`, &fakeDNS{})
	a.Store = nil
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "secret master key not configured") {
		t.Fatalf("err = %v", err)
	}
}

// TestDNSActionDecryptFailure 凭证密文解不开（换钥后旧密文）：报错留商家名。
func TestDNSActionDecryptFailure(t *testing.T) {
	a := setupDNSAction(t, `["1.1.1.1"]`, &fakeDNS{})
	if err := a.DB.Model(&storage.DNSProvider{}).Where("1=1").
		Update("api_key", "v1:not-valid:cipher").Error; err != nil {
		t.Fatal(err)
	}
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "dns provider cf") {
		t.Fatalf("err = %v", err)
	}
}

// TestInstanceActionTemplateErrors 模板与供应商三档缺失：原样上抛留原因。
func TestInstanceActionTemplateErrors(t *testing.T) {
	db := newTestDB(t)
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "ok", nil
	})
	launch := func(string) provision.LaunchParams {
		return provision.LaunchParams{NewToken: func() (string, error) { return "tok", nil }}
	}
	store := secret.NewStore("l3-master")

	// 模板不存在。
	a := &InstanceAction{Manager: m, Store: store, TemplateID: 99999, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "recovery template 99999") {
		t.Fatalf("missing template err = %v", err)
	}

	// 模板指向不存在的供应商。
	tpl := storage.ProvisionTemplate{Name: "t1", ProviderID: 99999}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	a = &InstanceAction{Manager: m, Store: store, TemplateID: tpl.ID, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "template provider missing") {
		t.Fatalf("missing provider err = %v", err)
	}

	// 供应商被禁用。
	store2 := secret.NewStore("l3-master")
	sealed, err := store2.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	// gorm 对带 default 的布尔零值跳写，禁用需显式更新。
	if err := db.Model(&prov).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	tpl2 := storage.ProvisionTemplate{Name: "t2", ProviderID: prov.ID}
	if err := db.Create(&tpl2).Error; err != nil {
		t.Fatal(err)
	}
	a = &InstanceAction{Manager: m, Store: store2, TemplateID: tpl2.ID, Launch: launch}
	if err := a.Run(context.Background(), storage.Node{Name: "n"}); err == nil ||
		!strings.Contains(err.Error(), "template provider disabled") {
		t.Fatalf("disabled provider err = %v", err)
	}
}

// TestInstanceActionApplyFailed 执行失败：状态非 ok 时留日志尾行报错。
func TestInstanceActionApplyFailed(t *testing.T) {
	db := newTestDB(t)
	store := secret.NewStore("l3-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "t3", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		if args[0] == "apply" {
			return "step1\nboom", context.DeadlineExceeded
		}
		return "inited", nil
	})
	a := &InstanceAction{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(string) provision.LaunchParams {
			return provision.LaunchParams{NewToken: func() (string, error) { return "tok", nil }}
		},
	}
	err = a.Run(context.Background(), storage.Node{Name: "entry-9"})
	if err == nil || !strings.Contains(err.Error(), "recovery apply failed: ") {
		t.Fatalf("err = %v; want last line of failed log", err)
	}
	// lastLine 取的是执行日志尾行：errString 把错误文本缀在末尾。
	if !strings.HasSuffix(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v; want log tail line", err)
	}
}

// TestStandbyActionNotWired 未接库的预备动作：失败留原因，不冒称成功。
func TestStandbyActionNotWired(t *testing.T) {
	a := &StandbyAction{}
	err := a.Run(context.Background(), storage.Node{Name: "n"})
	if err == nil || !strings.Contains(err.Error(), "standby action not wired") {
		t.Fatalf("err = %v", err)
	}
}
