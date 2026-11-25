package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 28 gap-fill: domain front (BR-2) 400/404/500 contracts -- DNS provider
// validation with missing name/type, list query failure, entry with bad binding
// / missing api_key / no master key / explicit disable / create failure, update
// bad id / missing / db-down 404, bad binding / unknown type / master-key-less
// rotate / save failure, delete bad id, unreferenced-success / delete failure,
// front validation seven rules direct-tested, list query failure, front create
// bad binding / missing provider / create failure, front update bad id / missing
// / bad binding / validation / missing provider / save failure, front delete bad
// id / missing / delete failure, geo-sync reconciliation db-down 500.
// Blank spots: none -- channel-behavior callouts are covered by the geodns
// package fake channel, out of scope for this batch.

// isDNSProviderModel tells whether the statement lands on the DNS provider table
// (the Model of a Find chain is a slice pointer).
func isDNSProviderModel(v any) bool {
	switch v.(type) {
	case *storage.DNSProvider, []storage.DNSProvider, *[]storage.DNSProvider:
		return true
	}
	return false
}

// isDNSFrontModel tells whether the statement lands on the front record table.
func isDNSFrontModel(v any) bool {
	switch v.(type) {
	case *storage.DNSFront, []storage.DNSFront, *[]storage.DNSFront:
		return true
	}
	return false
}

// failDNSProviderCreates / failDNSProviderUpdates / failDNSProviderDeletes make
// the create/update/delete statements of DNS provider rows fail.
func failDNSProviderCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:dnsprov_create", func(tx *gorm.DB) {
		if isDNSProviderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failDNSProviderUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:dnsprov_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.DNSProvider); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failDNSProviderDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:dnsprov_delete", func(tx *gorm.DB) {
		if isDNSProviderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failDNSFrontCreates / failDNSFrontUpdates / failDNSFrontDeletes make the
// create/update/delete statements of front rows fail.
func failDNSFrontCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:dnsfront_create", func(tx *gorm.DB) {
		if isDNSFrontModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failDNSFrontUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:dnsfront_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.DNSFront); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failDNSFrontDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:dnsfront_delete", func(tx *gorm.DB) {
		if isDNSFrontModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedDNSProvider inserts one DNS provider row directly (ciphertext fields hold a placeholder).
func seedDNSProvider(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.DNSProvider{Name: name, Type: "cloudflare", APIKey: "sealed", Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed dns provider %s: %v", name, err)
	}
	return row.ID
}

// seedDNSFront inserts one front record row directly.
func seedDNSFront(t *testing.T, db *gorm.DB, providerID uint, name string) uint {
	t.Helper()
	row := storage.DNSFront{Name: name, Domain: name + ".example.com", ProviderID: providerID,
		PrimaryIP: "10.0.0.1", BackupIPs: `["10.0.0.2"]`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed dns front %s: %v", name, err)
	}
	return row.ID
}

// TestListDNSProvidersFindFail DNS provider list query failure surfaces 500.
func TestListDNSProvidersFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/dns-providers", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDNSProviderErrors entry: bad binding, missing name/type, missing
// api_key, no master key, explicit disable and create failure.
func TestCreateDNSProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers",
		map[string]any{"type": "cloudflare", "api_key": "k"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "name and type are required") {
		t.Fatalf("missing fields: %d %s", rec.Code, rec.Body)
	}

	// api_key missing (only reached after name/type validation passed).
	r, db := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "api_key is required") {
		t.Fatalf("api_key: %d %s", rec.Code, rec.Body)
	}

	// no master key 500 (plaintext storage forbidden).
	r2, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r2, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare", "api_key": "k"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("no master key: %d %s", rec.Code, rec.Body)
	}

	// explicit disable persisted (Enabled carries default:true, so it needs an
	// explicit write-back to land false).
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers", map[string]any{
		"name": "cf-off", "type": "cloudflare", "api_key": "k", "enabled": false,
	}); rec.Code != http.StatusOK {
		t.Fatalf("disabled create: %d %s", rec.Code, rec.Body)
	}
	var row storage.DNSProvider
	if err := db.Where("name = ?", "cf-off").First(&row).Error; err != nil || row.Enabled {
		t.Fatalf("not persisted disabled: %+v err=%v", row, err)
	}

	// disable write-back failure 500.
	r, db = newTestRouterWithMasterKey(t)
	failDNSProviderUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers", map[string]any{
		"name": "wb-fail", "type": "cloudflare", "api_key": "k", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// create failure 500.
	r, db = newTestRouterWithMasterKey(t)
	failDNSProviderCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare", "api_key": "k"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDNSProviderErrors update: locate 404 (bad id / missing / db-down in
// the same contract), bad binding, unknown type, master-key-less rotate 500,
// save failure 500.
func TestUpdateDNSProviderErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	id := seedDNSProvider(t, db, "upd-dp")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns provider not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// missing and db-down share the same 404 contract.
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/9999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}
	r, db = newTestRouterWithMasterKey(t)
	seedDNSProvider(t, db, "upd-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/1", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}

	// bad binding and unknown type 400.
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "upd-bind")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)), "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "upd-bind", "type": "alidns"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	// master-key-less secret rotate 500 (validation passed first: name and type must be present).
	r2, db2 := newTestRouterWithDB(t)
	pid := seedDNSProvider(t, db2, "noseal")
	if rec := doJSON(t, r2, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(pid)),
		map[string]any{"name": "noseal", "type": "cloudflare", "api_key": "k-new"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("seal: %d %s", rec.Code, rec.Body)
	}

	// save failure 500.
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "upd-save")
	failDNSProviderUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "upd-save2", "type": "cloudflare"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteDNSProviderErrors delete: bad id 404, unreferenced success and
// delete failure 500 (the 409 for providers with fronts is already covered by
// the existing TestDNSAPI).
func TestDeleteDNSProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-providers/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// unreferenced provider deletes successfully.
	r, db := newTestRouterWithMasterKey(t)
	id := seedDNSProvider(t, db, "del-ok")
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.DNSProvider{}).Count(&n)
	if n != 0 {
		t.Fatalf("providers left: %d", n)
	}

	// delete failure 500.
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "del-fail")
	failDNSProviderDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateDNSFrontRules front validation's seven rules direct-tested branch
// by branch (each case rebuilds the input to avoid a shared pointer being
// written through).
func TestValidateDNSFrontRules(t *testing.T) {
	fresh := func() dnsFrontInput {
		pid := uint(1)
		return dnsFrontInput{Name: "主入口", Domain: "edge.example.com", ProviderID: &pid,
			PrimaryIP: "10.0.0.1", BackupIPs: `["10.0.0.2"]`}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*dnsFrontInput)
		msg    string
	}{
		{"domain-empty", func(in *dnsFrontInput) { in.Domain = "  " }, "domain is required"},
		{"domain-not-fqdn", func(in *dnsFrontInput) { in.Domain = "localhost" }, "domain must be a FQDN"},
		{"provider-nil", func(in *dnsFrontInput) { in.ProviderID = nil }, "provider_id is required"},
		{"provider-zero", func(in *dnsFrontInput) { *in.ProviderID = 0 }, "provider_id is required"},
		{"primary-ip", func(in *dnsFrontInput) { in.PrimaryIP = " " }, "primary_ip is required"},
		{"backup-empty", func(in *dnsFrontInput) { in.BackupIPs = " " }, "backup_ips is required"},
		{"backup-bad-json", func(in *dnsFrontInput) { in.BackupIPs = "10.0.0.2" }, "backup_ips must be a JSON string array"},
		{"backup-all-blank", func(in *dnsFrontInput) { in.BackupIPs = `["", "  "]` }, "backup_ips must contain at least one IP"},
	} {
		in := fresh()
		tc.mutate(&in)
		if err := validateDNSFront(in); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.msg)
		}
	}
	if err := validateDNSFront(fresh()); err != nil {
		t.Fatalf("valid base rejected: %v", err)
	}
}

// TestListDNSFrontsFindFail front list query failure surfaces 500.
func TestListDNSFrontsFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/dns-fronts", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDNSFrontErrors create: bad binding, missing DNS provider and create failure.
func TestCreateDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// DNS provider not found (location lookup only runs after validation passed).
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", map[string]any{
		"name": "n", "domain": "edge.example.com", "provider_id": 9999,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`,
	}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "dns provider not found") {
		t.Fatalf("provider: %d %s", rec.Code, rec.Body)
	}

	// create failure 500.
	pid := seedDNSProvider(t, db, "cf-create")
	failDNSFrontCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", map[string]any{
		"name": "n", "domain": "edge.example.com", "provider_id": pid,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDNSFrontErrors update: locate 404, bad binding, validation failure,
// missing DNS provider and save failure.
func TestUpdateDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	pid := seedDNSProvider(t, db, "cf-upd")
	fid := seedDNSFront(t, db, pid, "upd-f")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns front not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// missing 404.
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/9999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// bad binding, validation failure and missing DNS provider 400.
	r, db = newTestRouterWithMasterKey(t)
	pid = seedDNSProvider(t, db, "cf-upd2")
	fid = seedDNSFront(t, db, pid, "upd-f2")
	body := map[string]any{
		"name": "n", "domain": "edge2.example.com", "provider_id": pid,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`,
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/"+itoaUint(uint64(fid)), "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	bad := map[string]any{"name": "n", "domain": "edge2.example.com", "provider_id": pid,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`}
	bad["domain"] = "localhost"
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/"+itoaUint(uint64(fid)), bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}
	noProv := map[string]any{"name": "n", "domain": "edge2.example.com", "provider_id": 9999,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`}
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/"+itoaUint(uint64(fid)), noProv); rec.Code != http.StatusBadRequest {
		t.Fatalf("provider: %d %s", rec.Code, rec.Body)
	}

	// save failure 500.
	r, db = newTestRouterWithMasterKey(t)
	pid = seedDNSProvider(t, db, "cf-upd3")
	fid = seedDNSFront(t, db, pid, "upd-f3")
	failDNSFrontUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/"+itoaUint(uint64(fid)), body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteDNSFrontErrors delete: bad id and missing 404, delete failure 500
// (the 409 for switched-off fronts is already covered by the existing TestDNSAPI).
func TestDeleteDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	pid := seedDNSProvider(t, db, "cf-del")
	fid := seedDNSFront(t, db, pid, "del-f")
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/abc", nil); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns front not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// missing 404.
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// delete failure 500.
	r, db = newTestRouterWithMasterKey(t)
	pid = seedDNSProvider(t, db, "cf-del2")
	fid = seedDNSFront(t, db, pid, "del-f2")
	failDNSFrontDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/"+itoaUint(uint64(fid)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestGeoSyncDBError db-down during manual reconciliation: the first query failing surfaces 500.
func TestGeoSyncDBError(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts/geo-sync", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}
}
