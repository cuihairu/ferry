package provision

import (
	"context"
	"log"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// replenishFix 建模板/凭证/注入假 runner 的 Replenisher。
func replenishFix(t *testing.T, providerEnabled bool) (*Replenisher, *storage.ProvisionTemplate) {
	t.Helper()
	db := newTestDB(t)
	store := secret.NewStore("replenish-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: providerEnabled}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	if !providerEnabled {
		// GORM 对带 default 的布尔零值跳过写入，禁用需显式更新。
		if err := db.Model(&prov).Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
	}
	tpl := storage.ProvisionTemplate{Name: "refill", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "applied", nil
	})
	r := &Replenisher{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(name string) LaunchParams {
			return LaunchParams{InstanceName: name, PanelWSURL: "ws://p", NewToken: func() (string, error) { return "tok-" + name, nil }}
		},
	}
	return r, &tpl
}

func countProvisioning(t *testing.T, r *Replenisher) int64 {
	t.Helper()
	var n int64
	r.Manager.DB().Model(&storage.Node{}).
		Where("name LIKE ?", "pool-%").Count(&n)
	return n
}

// TestReplenishSweep 覆盖池空保底：池空且无在途 → 自动开机（节点行
// provisioning、job 留痕）；池未空或在途不开；未配置模板不开。
func TestReplenishSweep(t *testing.T) {
	r, _ := replenishFix(t, true)
	logger := log.Default()

	// 未配置模板：永不触发。
	r.TemplateID = 0
	if fired, err := r.Sweep(context.Background(), logger); err != nil || fired {
		t.Fatalf("disabled fired=%v err=%v", fired, err)
	}
	r.TemplateID = 999 // 不存在的模板同口径不开
	if fired, err := r.Sweep(context.Background(), logger); err == nil || fired {
		t.Fatalf("missing template fired=%v err=%v", fired, err)
	}
	r.TemplateID = 1

	// 池里有 active 入口：不开。
	db := r.Manager.DB()
	if err := db.Create(&storage.Node{Name: "in-pool", Token: "t1", Role: "entry",
		Enabled: true, Status: "online", PoolState: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if fired, err := r.Sweep(context.Background(), logger); err != nil || fired {
		t.Fatalf("non-empty pool fired=%v err=%v", fired, err)
	}

	// 池空：开机（节点行 provisioning，OS-4 流水线接管入池）。
	if err := db.Where("name = ?", "in-pool").Delete(&storage.Node{}).Error; err != nil {
		t.Fatal(err)
	}
	fired, err := r.Sweep(context.Background(), logger)
	if err != nil || !fired {
		t.Fatalf("empty pool fired=%v err=%v", fired, err)
	}
	deadline := 100
	for countProvisioning(t, r) == 0 && deadline > 0 {
		deadline--
		if deadline == 0 {
			t.Fatal("no provisioning node created")
		}
	}
	// 执行在后台 goroutine，轮询等 job 回填终态。
	var job storage.ProvisionJob
	ok := false
	for i := 0; i < 100; i++ {
		if err := db.First(&job).Error; err != nil {
			t.Fatal(err)
		}
		if job.Status == "ok" {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("job never finished: %+v", job)
	}

	// 在途防重：provisioning 入口存在（上一轮刚开的）且池空 → 不再开。
	fired, err = r.Sweep(context.Background(), logger)
	if err != nil || fired {
		t.Fatalf("in-flight must block, fired=%v err=%v", fired, err)
	}
}

// TestReplenishProviderDisabled 模板提供商停用：报错不开机不冒称触发。
func TestReplenishProviderDisabled(t *testing.T) {
	r, _ := replenishFix(t, false)
	_, err := r.Sweep(context.Background(), log.Default())
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("err = %v", err)
	}
}
