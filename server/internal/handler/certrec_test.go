package handler

// 证书任务与恢复流水线查询面缝隙批：cert-tasks CRUD 与手动触发的
// 404/409 分支、recoveries 两查询端点此前零覆盖。issue 正常路径
// 会真调 acme.sh（cert.IssueNow 已由 cert 包自测），本批不触发。

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

// TestCertTasksCRUD 覆盖证书任务校验、创建、维护与删除。
func TestCertTasksCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: "tok"}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	// 校验负例：空域名/非 FQDN/非法 method/dns-01 缺 provider/provider 不存在。
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
	// 合法 dns-01（带 provider）与 http-01（免 provider）。
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
	// 维护：改域名与 method，applied 任务重置回 pending 等循环重签。
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
	// 删除后不再在列表；触发类端点只测 404/409（正常路径真调 acme.sh）。
	if rec = doJSON(t, r, "DELETE", "/api/cert-tasks/"+itoa(tasks[0].ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/9999/issue", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("issue missing: %d, want 404", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/abc/issue", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("issue bad id: %d, want 404", rec.Code)
	}
	// issuing 抢占防重发：任务已 issuing 时 409，不进 IssueNow。
	issuing := storage.CertTask{Name: "i", Domain: "i.example.com", Method: "dns-01",
		DNSProviderID: prov.ID, State: "issuing"}
	if err := db.Create(&issuing).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "POST", "/api/cert-tasks/"+itoa(issuing.ID)+"/issue", nil); rec.Code != http.StatusConflict {
		t.Fatalf("issue issuing: %d, want 409", rec.Code)
	}
}

// TestRecoveriesQuery 覆盖恢复流水线列表与动作回放查询。
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
