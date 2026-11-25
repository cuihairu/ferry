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

// TestProvisionRunAPI covers provisioning execution (OS-2): a template-triggered
// plan/apply returns the running job immediately, background execution backfills
// the final state, the validation chain (no credentials / no master key / disabled
// provider refused), and the job trail list.
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
	cfg.TofuWorkdir = t.TempDir() // the workdir must never be a relative path, and tests must not pollute the package directory
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

	// disabled provider refused
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

	// apply: returns a running job immediately (it can even respond without the
	// real tofu present — execution is in the background and the trail ends up
	// failed)
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

	// OS-3: apply pre-mints the node row — provisioning state, token ready,
	// metadata taken from the template.
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
	// plan creates no nodes.
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

	// replaying apply (same name) reuses the existing row: token unchanged, no
	// duplicate row.
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

	// the background goroutine will backfill failed because tofu is missing —
	// poll until the final state lands.
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

	// plan follows the same convention + the jobs list
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

	// missing template 404
	rec = doJSON(t, r, "POST", "/api/provision-templates/424242/apply", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing template status = %d", rec.Code)
	}

	// Wait for every background job to reach a final state before wrapping up:
	// above we only polled the first apply's job, and the re-apply and plan
	// background goroutines may still be writing TofuWorkdir; once the test
	// returns, the t.TempDir cleanup races them (sporadic "unlinkat directory
	// not empty"). A job reaching its final state means Execute has finished all
	// disk writes, so nothing is written afterwards.
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

// TestListProvisionJobsLimit the trail list's pagination convention: a valid
// limit is honored; non-numeric/non-positive/out-of-range all 400.
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

	// limit=5: valid and returns an array (an empty DB yields an empty slice,
	// not null).
	rec := doJSON(t, r, "GET", "/api/provision-jobs?limit=5", nil)
	if rec.Body.String() == "null\n" {
		t.Fatalf("empty list should be [], got %s", rec.Body)
	}
}

// regQueryFail injects an error keyed on the query's destination type (a gorm
// callback, matching the existing error-injection convention); other cases in
// the package reuse it. Pass dest as a (*T)(nil) type placeholder.
func regQueryFail(name string, db *gorm.DB, dest any) {
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if reflect.TypeOf(tx.Statement.Dest) == reflect.TypeOf(dest) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// provisionFixture starts a router with a master key and lands a usable template
// plus credentials, returning the router, the DB, and the template id (the
// trigger chain needs to decrypt the ciphertext and a valid provider).
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

// TestProvisionRunErrors covers the trigger chain's validation and DB-failure
// branches: bad id 404, template pointing at a missing provider 400, no master
// key 400, undecryptable credentials 400, apply's pre-mint job creation failing
// 500, plan's job creation failing 500.
func TestProvisionRunErrors(t *testing.T) {
	r, db, _ := provisionFixture(t)

	// bad id -> 404 (never enters the validation chain).
	if rec := doJSON(t, r, "POST", "/api/provision-templates/abc/plan", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}
	// template pointing at a missing provider -> 400.
	ghost := storage.ProvisionTemplate{Name: "ghost", ProviderID: 999, Plan: "x"}
	if err := db.Create(&ghost).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/provision-templates/"+itoa64(int(ghost.ID))+"/plan", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider: %d", rec.Code)
	}

	// no master key -> 400 (the default test config leaves SecretKey empty).
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

	// undecryptable credentials -> 400.
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

	// apply's pre-mint job creation failing -> 500 (the PrepareLaunch job step
	// errors out).
	r4, db4, tpl4 := provisionFixture(t)
	db4.Callback().Create().Before("gorm:create").Register("t:jobcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.ProvisionJob); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r4, "POST", "/api/provision-templates/"+itoa64(int(tpl4))+"/apply", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("apply prepare: %d %s", rec.Code, rec.Body)
	}

	// plan's job creation failing -> 500.
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

// TestRecoveryListErrors covers the recovery pipeline trail list: limit negative
// cases and read failures, action replay with a bad id, and read failures.
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

	// all three read paths failing -> 500.
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
