package handler

// Gap-fill batch for the cert task and recovery pipeline query surface: cert-task
// CRUD and the 404/409 branches of the manual trigger endpoints, plus the two
// recoveries query endpoints, had zero coverage. The issue happy path really
// shells out to acme.sh (cert.IssueNow is already tested inside the cert
// package), so this batch does not trigger it.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/cert"
	"github.com/cuihairu/ferry/server/internal/storage"
)

func itoa(n uint) string { return strconv.FormatUint(uint64(n), 10) }

// TestCertTasksCRUD covers cert-task validation, creation, update and deletion.
func TestCertTasksCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: "tok"}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	// Validation negative cases: empty domain / non-FQDN / invalid method /
	// dns-01 without a provider / provider not found.
	for _, body := range []map[string]any{
		{"domain": "", "method": "dns-01", "provider_id": prov.ID},
		{"domain": "localhost", "method": "dns-01", "provider_id": prov.ID},
		{"domain": "edge.example.com", "method": "tls-alpn-01", "provider_id": prov.ID},
		{"domain": "edge.example.com", "method": "dns-01"},
		{"domain": "edge.example.com", "method": "dns-01", "provider_id": 9999},
	} {
		if rec := doJSON(t, r, "POST", "/api/cert-tasks", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("create %v: %d, want 400", body, rec.Code)
		}
	}
	// Valid dns-01 (with a provider) and http-01 (no provider needed).
	if rec := doJSON(t, r, "POST", "/api/cert-tasks",
		map[string]any{"name": "edge", "domain": "edge.example.com", "method": "dns-01", "provider_id": prov.ID}); rec.Code != http.StatusOK {
		t.Fatalf("create dns-01: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/cert-tasks",
		map[string]any{"name": "alt", "domain": "alt.example.com", "method": "http-01"}); rec.Code != http.StatusOK {
		t.Fatalf("create http-01: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "GET", "/api/cert-tasks", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var tasks []storage.CertTask
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID >= tasks[1].ID || tasks[0].State != cert.StatePending {
		t.Fatalf("tasks = %+v, want 2 rows id ASC state pending", tasks)
	}
	// Update: change domain and method; an applied task is reset to pending so the
	// renewal loop re-signs it.
	if err := db.Model(&storage.CertTask{}).Where("id = ?", tasks[0].ID).Update("state", "applied").Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "PUT", "/api/cert-tasks/"+itoa(tasks[0].ID),
		map[string]any{"domain": "edge2.example.com", "method": "http-01"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var row storage.CertTask
	if err := db.First(&row, tasks[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Domain != "edge2.example.com" || row.Method != "http-01" || row.State != cert.StatePending {
		t.Fatalf("updated = %+v", row)
	}
	// No longer listed after delete; only the 404/409 branches of the trigger
	// endpoints are tested (the happy path really calls acme.sh).
	if rec = doJSON(t, r, "DELETE", "/api/cert-tasks/"+itoa(tasks[0].ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/9999/issue", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("issue missing: %d, want 404", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/abc/issue", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("issue bad id: %d, want 404", rec.Code)
	}
	// Issuing-state claim prevents double issuance: 409 when the task is already
	// issuing, and IssueNow is not reached.
	issuing := storage.CertTask{Name: "i", Domain: "i.example.com", Method: "dns-01",
		DNSProviderID: prov.ID, State: "issuing"}
	if err := db.Create(&issuing).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/"+itoa(issuing.ID)+"/issue", nil); rec.Code != http.StatusConflict {
		t.Fatalf("issue issuing: %d, want 409", rec.Code)
	}
}

// TestRecoveriesQuery covers the recovery pipeline list and action replay queries.
func TestRecoveriesQuery(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	rec1 := storage.Recovery{NodeID: 1, NodeName: "n1", Level: 2, State: "running", StartedAt: now, UpdatedAt: now}
	rec2 := storage.Recovery{NodeID: 2, NodeName: "n2", Level: 1, State: "ok", StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if err := db.Create(&storage.Recovery{NodeID: 3, NodeName: "n3"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&rec1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&rec2).Error; err != nil {
		t.Fatal(err)
	}
	for _, a := range []storage.RecoveryAction{
		{RecoveryID: rec1.ID, NodeID: 1, Level: 1, Action: "dns-switch", State: "ok", StartedAt: now},
		{RecoveryID: rec1.ID, NodeID: 1, Level: 2, Action: "probe-wait", State: "running", StartedAt: now},
		{RecoveryID: rec2.ID, NodeID: 2, Level: 1, Action: "dns-switch", State: "ok", StartedAt: now},
	} {
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, "GET", "/api/recoveries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.Recovery
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 3 || rows[0].NodeName != "n2" || rows[1].NodeName != "n1" || rows[2].NodeName != "n3" {
		t.Fatalf("rows = %+v, want id DESC (n2,n1,n3)", rows)
	}
	for _, bad := range []string{"limit=0", "limit=201", "limit=abc"} {
		if rec = doJSON(t, r, "GET", "/api/recoveries?"+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", bad, rec.Code)
		}
	}
	rec = doJSON(t, r, "GET", "/api/recoveries/"+itoa(rec1.ID)+"/actions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("actions: %d %s", rec.Code, rec.Body)
	}
	var acts []storage.RecoveryAction
	if err := json.Unmarshal(rec.Body.Bytes(), &acts); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(acts) != 2 || acts[0].Level != 1 || acts[1].Level != 2 {
		t.Fatalf("acts = %+v, want rec1 actions level ASC", acts)
	}
	if rec = doJSON(t, r, "GET", "/api/recoveries/9999/actions", nil); rec.Code != http.StatusOK {
		t.Fatalf("unknown recovery actions: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &acts); err != nil || len(acts) != 0 {
		t.Fatalf("unknown acts = %d rows", len(acts))
	}
	if rec = doJSON(t, r, "GET", "/api/recoveries/abc/actions", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d, want 400", rec.Code)
	}
}
