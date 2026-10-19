package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestProvisionRunAPI 覆盖供给执行（OS-2）：模板触发 plan/apply 立即回
// running job、后台执行回填终态、校验链（无凭证/无主密钥/停用提供商拒绝）、
// job 留痕列表。
func TestProvisionRunAPI(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	cfg.TofuWorkdir = t.TempDir() // 工作目录绝不落相对路径，测试也不污染包目录
	r, _ := NewRouter(db, cfg)
	store := secret.NewStore(cfg.SecretKey)

	prov := storage.Provider{Name: "vultr", Type: "vultr", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov.AccessKey = sealed
	if err := db.Save(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "hk-3t", ProviderID: prov.ID, Plan: "vc2-1c-1gb",
		Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry", Config: `{"inbounds":[]}`}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}

	// 停用提供商拒绝
	disabled := prov
	disabled.Enabled = false
	if err := db.Model(&storage.Provider{}).Where("id = ?", prov.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(tpl.ID))+"/apply", map[string]any{"name": "n1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("disabled provider status = %d body=%s", rec.Code, rec.Body)
	}
	db.Model(&storage.Provider{}).Where("id = ?", prov.ID).Update("enabled", true)

	// apply：立即回 running job（真实 tofu 不存在也能回——执行在后台，留痕 failed）
	rec = doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(tpl.ID))+"/apply", map[string]any{"name": "ferry-hk-3t"})
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s", rec.Code, rec.Body)
	}
	var job struct {
		ID     uint   `json:"id"`
		Status string `json:"status"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != "running" || job.Action != "apply" {
		t.Fatalf("job = %+v", job)
	}

	// OS-3：apply 预签发节点行——provisioning 态、token 已备、元数据取自模板。
	var node storage.Node
	if err := db.Where("name = ?", "ferry-hk-3t").First(&node).Error; err != nil {
		t.Fatalf("provision node not created: %v", err)
	}
	if node.Status != "provisioning" || node.Token == "" || !node.Enabled || !node.MetaInit {
		t.Fatalf("provision node = %+v", node)
	}
	if node.Role != "entry" || node.Direction != "out" {
		t.Fatalf("node meta from template: %+v", node)
	}
	if node.Config != `{"inbounds":[]}` {
		t.Fatalf("config template must copy to node: %q", node.Config)
	}
	// plan 不建节点。
	before := int64(0)
	db.Model(&storage.Node{}).Count(&before)
	rec = doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(tpl.ID))+"/plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d", rec.Code)
	}
	after := int64(0)
	db.Model(&storage.Node{}).Count(&after)
	if before != after {
		t.Fatalf("plan must not create nodes: %d -> %d", before, after)
	}

	// 重放 apply（同名）复用既有行：token 不变，不重复建行。
	rec = doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(tpl.ID))+"/apply", map[string]any{"name": "ferry-hk-3t"})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-apply status = %d", rec.Code)
	}
	var again storage.Node
	if err := db.Where("name = ?", "ferry-hk-3t").First(&again).Error; err != nil {
		t.Fatal(err)
	}
	if again.Token != node.Token {
		t.Fatalf("re-apply must reuse node token: %q vs %q", again.Token, node.Token)
	}

	// 后台 goroutine 会因 tofu 不存在回填 failed——轮询等终态落库。
	deadline := time.Now().Add(10 * time.Second)
	for {
		var row storage.ProvisionJob
		if err := db.First(&row, job.ID).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status != "running" {
			if row.Status != "failed" || row.Log == "" {
				t.Fatalf("job row = %+v", row)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job never finished")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// plan 同口径 + jobs 列表
	rec = doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(tpl.ID))+"/plan", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("plan status = %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/provision-jobs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs status = %d", rec.Code)
	}
	var jobs []storage.ProvisionJob
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) < 2 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	if jobs[0].CreatedAt.Before(jobs[len(jobs)-1].CreatedAt) && jobs[0].ID < jobs[len(jobs)-1].ID {
		t.Fatalf("jobs not desc: first=%+v last=%+v", jobs[0], jobs[len(jobs)-1])
	}

	// 不存在的模板 404
	rec = doJSON(t, r, "POST", "/api/provision-templates/424242/apply", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing template status = %d", rec.Code)
	}

	// 收尾前等全部后台 job 落终态：上面只轮询了首个 apply 的 job，re-apply
	// 与 plan 的后台 goroutine 可能还在写 TofuWorkdir，测试一返回 t.TempDir
	// 清理就与之竞态（unlinkat directory not empty 偶发）。job 落终态即
	// Execute 的写盘全部结束，此后无再写入。
	deadline = time.Now().Add(10 * time.Second)
	for {
		var running int64
		if err := db.Model(&storage.ProvisionJob{}).Where("status = ?", "running").Count(&running).Error; err != nil {
			t.Fatal(err)
		}
		if running == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background jobs never finished")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func itoa64(n int) string {
	return strconv.FormatInt(int64(n), 10)
}

// TestListProvisionJobsLimit 留痕列表分页口径：limit 合法生效、
// 非数字/非正数/越界一律 400。
func TestListProvisionJobsLimit(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	for path, want := range map[string]int{
		"/api/provision-jobs":           http.StatusOK,
		"/api/provision-jobs?limit=5":   http.StatusOK,
		"/api/provision-jobs?limit=abc": http.StatusBadRequest,
		"/api/provision-jobs?limit=0":   http.StatusBadRequest,
		"/api/provision-jobs?limit=201": http.StatusBadRequest,
	} {
		rec := doJSON(t, r, "GET", path, nil)
		if rec.Code != want {
			t.Fatalf("%s: status = %d body=%s", path, rec.Code, rec.Body)
		}
	}

	// limit=5：合法且返回数组（空库返回空切片而非 null）。
	rec := doJSON(t, r, "GET", "/api/provision-jobs?limit=5", nil)
	if rec.Body.String() == "null\n" {
		t.Fatalf("empty list should be [], got %s", rec.Body)
	}
}

// regQueryFail 按查询目标类型注错（gorm 回调，对齐既有错误注入口径）；
// 同包其他用例复用。dest 传 (*T)(nil) 形式的类型占位。
func regQueryFail(name string, db *gorm.DB, dest any) {
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if reflect.TypeOf(tx.Statement.Dest) == reflect.TypeOf(dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// provisionFixture 起带主密钥的路由并落一个可用模板与凭证，返回路由、库、
// 模板 id（触发链需要解密密文与有效提供商）。
func provisionFixture(t *testing.T) (*gin.Engine, *gorm.DB, uint) {
	t.Helper()
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	prov := storage.Provider{Name: "vultr", Type: "vultr", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	sealed, err := secret.NewStore("test-master-key").Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov.AccessKey = sealed
	if err := db.Save(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "hk-3t", ProviderID: prov.ID, Plan: "vc2-1c-1gb",
		Region: "hkg", Config: `{"inbounds":[]}`}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	return r, db, tpl.ID
}

// TestProvisionRunErrors 覆盖触发链各校验与库故障分支：坏 id 404、模板指向
// 不存在提供商 400、无主密钥 400、凭证解不开 400、apply 预签发建 job 失败 500、
// plan 建档失败 500。
func TestProvisionRunErrors(t *testing.T) {
	r, db, _ := provisionFixture(t)

	// 坏 id → 404（不进校验链）。
	if rec := doJSON(t, r, "POST", "/api/provision-templates/abc/plan", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// 模板指向不存在提供商 → 400。
	ghost := storage.ProvisionTemplate{Name: "ghost", ProviderID: 999, Plan: "x"}
	if err := db.Create(&ghost).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(ghost.ID))+"/plan", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider: %d", rec.Code)
	}

	// 无主密钥 → 400（默认测试配置 SecretKey 为空）。
	r2, db2 := newTestRouterWithDB(t)
	if err := db2.Create(&storage.Provider{Name: "v2", Type: "vultr", Enabled: true, AccessKey: "plain"}).Error; err != nil {
		t.Fatal(err)
	}
	tpl2 := storage.ProvisionTemplate{Name: "hk-2t", ProviderID: 1, Plan: "x", Config: `{}`}
	if err := db2.Create(&tpl2).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r2, "POST", "/api/provision-templates/"+itoa64(int(tpl2.ID))+"/plan", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("no master key: %d %s", rec.Code, rec.Body)
	}

	// 凭证解不开 → 400。
	r3, db3 := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	if err := db3.Create(&storage.Provider{Name: "v3", Type: "vultr", Enabled: true, AccessKey: "not-a-sealed-key"}).Error; err != nil {
		t.Fatal(err)
	}
	tpl3 := storage.ProvisionTemplate{Name: "hk-3t", ProviderID: 1, Plan: "x", Config: `{}`}
	if err := db3.Create(&tpl3).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r3, "POST", "/api/provision-templates/"+itoa64(int(tpl3.ID))+"/plan", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("undecryptable: %d %s", rec.Code, rec.Body)
	}

	// apply 预签发建 job 失败 → 500（PrepareLaunch 建 job 步冒错）。
	r4, db4, tpl4 := provisionFixture(t)
	db4.Callback().Create().Before("gorm:create").Register("t:jobcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.ProvisionJob); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r4, "POST", "/api/provision-templates/"+itoa64(int(tpl4))+"/apply", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply prepare: %d %s", rec.Code, rec.Body)
	}

	// plan 建档失败 → 500。
	r5, db5, tpl5 := provisionFixture(t)
	db5.Callback().Create().Before("gorm:create").Register("t:plancreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.ProvisionJob); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r5, "POST", "/api/provision-templates/"+itoa64(int(tpl5))+"/plan", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("plan create: %d %s", rec.Code, rec.Body)
	}
}

// TestRecoveryListErrors 覆盖恢复流水线留痕列表：limit 负例与取数失败、
// 动作回放坏 id 与取数失败。
func TestRecoveryListErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=201"} {
		if rec := doJSON(t, r, "GET", "/api/recoveries"+q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("recoveries %s: %d", q, rec.Code)
		}
	}
	if rec := doJSON(t, r, "GET", "/api/recoveries?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("recoveries ok: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/recoveries/abc/actions", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("actions bad id: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/recoveries/0/actions", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("actions zero id: %d", rec.Code)
	}

	// 三路取数失败 → 500。
	sets := []struct {
		name string
		path string
		dest any
	}{
		{"jobs", "/api/provision-jobs", (*[]storage.ProvisionJob)(nil)},
		{"recoveries", "/api/recoveries", (*[]storage.Recovery)(nil)},
		{"actions", "/api/recoveries/1/actions", (*[]storage.RecoveryAction)(nil)},
	}
	for _, sc := range sets {
		r6, db6 := newTestRouterWithDB(t)
		regQueryFail("t:recon", db6, sc.dest)
		if rec := doJSON(t, r6, "GET", sc.path, nil); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s query fail: %d %s", sc.name, rec.Code, rec.Body)
		}
	}
}
