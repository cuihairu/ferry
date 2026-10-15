package handler

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/cert"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 三十八批补口：证书任务（BR-4）错误口径——列表取数失败、建号坏绑定/缺省
// method 落 dns-01/落库失败、更新坏 id/库故障定位（口径同 404）/坏绑定/
// 校验失败/DNS 商查无 400/DNS 商命中回写/缺省 method 回写/保存失败、删除
// 坏 id/落删失败、签发更新失败/IssueNow 取行失败/签发成功立返 200。
// 留白：无（签发成功后真实签发在 goroutine 内异步跑，失败只记日志，不
// 影响本端点口径）。

// isCertTaskModel 判断语句是否落在证书任务表上。
func isCertTaskModel(v any) bool {
	switch v.(type) {
	case *storage.CertTask, []storage.CertTask, *[]storage.CertTask:
		return true
	}
	return false
}

// seedCertTaskRow 直落一行证书任务（HTTP-01 无需 DNS 商凭证）。
func seedCertTaskRow(t *testing.T, db *gorm.DB, name, state string) uint {
	t.Helper()
	row := storage.CertTask{Name: name, Domain: name + ".example.com",
		Method: cert.MethodHTTP01, State: state}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed cert task %s: %v", name, err)
	}
	return row.ID
}

// TestListCertTasksError 列表取数失败 500。
func TestListCertTasksError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:certtask_q", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/cert-tasks", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateCertTaskErrors 建号：坏绑定、缺省 method 落 dns-01 与落库失
// 败。
func TestCreateCertTaskErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	// 缺 method：校验允许空串，落库前归一为 dns-01。
	rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks",
		map[string]any{"name": "d1", "domain": "d1.example.com"})
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"method":"dns-01"`) {
		t.Fatalf("method default: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:certtask_create", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks",
		map[string]any{"name": "d2", "domain": "d2.example.com", "method": "http-01"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateCertTaskErrors 更新：坏 id、库故障定位、坏绑定、校验失败、
// DNS 商查无、DNS 商命中回写、缺省 method 回写与保存失败。
func TestUpdateCertTaskErrors(t *testing.T) {
	valid := func(domain string) map[string]any {
		return map[string]any{"name": "u", "domain": domain, "method": "http-01"}
	}

	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/abc", valid("u.example.com")); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 库故障定位：本端点把 First 一切错误都归 404。
	r, db := newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u1", cert.StatePending)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1", valid("u1.example.com")); rec.Code != http.StatusNotFound {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u2", cert.StatePending)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1",
		map[string]any{"domain": "nodot", "method": "tls-01"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1",
		map[string]any{"domain": "u2.example.com", "method": "dns-01", "provider_id": 9999}); rec.Code != http.StatusBadRequest {
		t.Fatalf("provider missing: %d %s", rec.Code, rec.Body)
	}

	// DNS 商命中：回写 provider_id 并重置待签。
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u3", "applied")
	pid := seedDNSProvider(t, db, "cert-p")
	b := valid("u3.example.com")
	b["method"] = "dns-01"
	b["provider_id"] = pid
	rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1", b)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"provider_id":1`) ||
		!containsStr(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("provider hit: %d %s", rec.Code, rec.Body)
	}

	// 缺 method 回写：归一为 dns-01。
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u4", "applied")
	rec = doJSON(t, r, http.MethodPut, "/api/cert-tasks/1",
		map[string]any{"name": "u4", "domain": "u4.example.com"})
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"method":"dns-01"`) {
		t.Fatalf("method default: %d %s", rec.Code, rec.Body)
	}

	// 保存失败。
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u5", cert.StatePending)
	failUpdateOn(t, db, "test:certtask_save", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1", valid("u5.example.com")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteCertTaskErrors 删除：坏 id 与落删失败。
func TestDeleteCertTaskErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/cert-tasks/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "d1", cert.StatePending)
	db.Callback().Delete().Before("gorm:delete").Register("test:certtask_delete", func(tx *gorm.DB) {
		if isCertTaskModel(tx.Statement.Model) || isCertTaskModel(tx.Statement.Dest) {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r, http.MethodDelete, "/api/cert-tasks/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}

// TestIssueCertTaskErrors 签发：抢占更新失败、IssueNow 取行失败与签发成
// 功立返 200。
func TestIssueCertTaskErrors(t *testing.T) {
	// 抢占更新失败（同事务条件更新落库故障）。
	r, db := newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "i1", cert.StatePending)
	failUpdateOn(t, db, "test:certtask_issue_update", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks/1/issue", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// IssueNow 取行失败：处理器定位（第 1 查）放行，IssueNow 取行（第 2 查）注错。
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "i2", cert.StatePending)
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:certtask_issue_q", func(tx *gorm.DB) {
		if isCertTaskModel(tx.Statement.Model) || isCertTaskModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks/1/issue", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("issue now: %d %s", rec.Code, rec.Body)
	}

	// 签发成功：任务行立返 200（真实签发异步跑，无 acme.sh 时仅记日志）。
	r, db = newTestRouterWithDB(t)
	id := seedCertTaskRow(t, db, "i3", cert.StatePending)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks/"+strconv.Itoa(int(id))+"/issue", nil); rec.Code != http.StatusOK {
		t.Fatalf("issue ok: %d %s", rec.Code, rec.Body)
	}
}
