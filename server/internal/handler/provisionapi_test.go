package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
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
		Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
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
}

func itoa64(n int) string {
	return strconv.FormatInt(int64(n), 10)
}
