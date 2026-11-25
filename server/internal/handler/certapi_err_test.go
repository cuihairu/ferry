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

// Batch 38 gap-filling: error paths for cert tasks (BR-4) — list fetch
// failure, create with bad binding / default method landing on dns-01 /
// persist failure, update with bad id / locate failure under a DB fault
// (same convention as 404) / bad binding / validation failure / DNS provider
// not found 400 / DNS provider hit written back / default method written back /
// save failure, delete with bad id and persist failure, and issue with update
// failure / IssueNow fetch failure / successful issuance returning 200
// immediately.
// Gaps left: none (after a successful issue the real issuance runs
// asynchronously in a goroutine; failures are only logged and do not affect
// this endpoint's convention).

// isCertTaskModel reports whether a statement lands on the cert task table.
func isCertTaskModel(v any) bool {
	switch v.(type) {
	case *storage.CertTask, []storage.CertTask, *[]storage.CertTask:
		return true
	}
	return false
}

// seedCertTaskRow inserts one cert task row directly (HTTP-01 needs no DNS provider credentials).
func seedCertTaskRow(t *testing.T, db *gorm.DB, name, state string) uint {
	t.Helper()
	row := storage.CertTask{Name: name, Domain: name + ".example.com",
		Method: cert.MethodHTTP01, State: state}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed cert task %s: %v", name, err)
	}
	return row.ID
}

// TestListCertTasksError: list fetch failure returns 500.
func TestListCertTasksError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:certtask_q", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/cert-tasks", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateCertTaskErrors: create — bad binding, the default method landing
// on dns-01, and persist failure.
func TestCreateCertTaskErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	// Missing method: validation allows the empty string, normalized to dns-01 before persisting.
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

// TestUpdateCertTaskErrors: update — bad id, locate failure under a DB fault,
// bad binding, validation failure, DNS provider not found, DNS provider hit
// written back, default method written back, and save failure.
func TestUpdateCertTaskErrors(t *testing.T) {
	valid := func(domain string) map[string]any {
		return map[string]any{"name": "u", "domain": domain, "method": "http-01"}
	}

	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/abc", valid("u.example.com")); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// Locate failure under a DB fault: this endpoint folds every First error into 404.
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

	// DNS provider hit: provider_id is written back and the task is reset to pending.
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

	// Missing method written back: normalized to dns-01.
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u4", "applied")
	rec = doJSON(t, r, http.MethodPut, "/api/cert-tasks/1",
		map[string]any{"name": "u4", "domain": "u4.example.com"})
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"method":"dns-01"`) {
		t.Fatalf("method default: %d %s", rec.Code, rec.Body)
	}

	// Save failure.
	r, db = newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "u5", cert.StatePending)
	failUpdateOn(t, db, "test:certtask_save", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodPut, "/api/cert-tasks/1", valid("u5.example.com")); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteCertTaskErrors: delete — bad id and persist failure.
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

// TestIssueCertTaskErrors: issue — the claim update failing, IssueNow failing
// to fetch the row, and a successful issue returning 200 immediately.
func TestIssueCertTaskErrors(t *testing.T) {
	// Claim update failure (a DB fault on the conditional update in the same transaction).
	r, db := newTestRouterWithDB(t)
	seedCertTaskRow(t, db, "i1", cert.StatePending)
	failUpdateOn(t, db, "test:certtask_issue_update", isCertTaskModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks/1/issue", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// IssueNow fetch failure: let the handler's locate query (1st) pass and inject the error into IssueNow's row fetch (2nd query).
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

	// Successful issue: the task row returns 200 immediately (the real issuance runs asynchronously and only logs when acme.sh is missing).
	r, db = newTestRouterWithDB(t)
	id := seedCertTaskRow(t, db, "i3", cert.StatePending)
	if rec := doJSON(t, r, http.MethodPost, "/api/cert-tasks/"+strconv.Itoa(int(id))+"/issue", nil); rec.Code != http.StatusOK {
		t.Fatalf("issue ok: %d %s", rec.Code, rec.Body)
	}
}
