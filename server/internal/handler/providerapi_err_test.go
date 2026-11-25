package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch twenty-seven gap fill: the 400/404/500 conventions of provisioning
// config (OS-1) — provider list read failure; entry with bad bind/missing
// fields/explicit disable/persist failure; update lookup 404 (bad id, not-found
// and DB failure share one convention) / bad bind / type and switch branches /
// rotating the secret without a master key / save failure; delete lookup and
// delete-persist failures; template validation rules; list read failure;
// template creation with bad bind / role defaulting / persist failure; template
// update lookup 404 / bad bind / validation failure / bandwidth and monthly-cost
// branches / save failure; template delete lookup and delete-persist failures.
// Left out: the template list success path is already covered by the existing
// TestTemplateAPI; only the error branches are added here.

// isProviderModel reports whether the statement targets the provider table
// (Find chains pass Model as a slice pointer).
func isProviderModel(v any) bool {
	switch v.(type) {
	case *storage.Provider, []storage.Provider, *[]storage.Provider:
		return true
	}
	return false
}

// isTplModel reports whether the statement targets the machine template table.
func isTplModel(v any) bool {
	switch v.(type) {
	case *storage.ProvisionTemplate, []storage.ProvisionTemplate, *[]storage.ProvisionTemplate:
		return true
	}
	return false
}

// failProvidersCreates / failProvidersUpdates / failProvidersDeletes make the
// provider rows' create/update/delete statements fail.
func failProvidersCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:provider_create", func(tx *gorm.DB) {
		if isProviderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failProvidersUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:provider_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Provider); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failProvidersDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:provider_delete", func(tx *gorm.DB) {
		if isProviderModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failTplCreates / failTplUpdates / failTplDeletes make the template rows'
// create/update/delete statements fail.
func failTplCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:tpl_create", func(tx *gorm.DB) {
		if isTplModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failTplUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:tpl_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.ProvisionTemplate); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func failTplDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:tpl_delete", func(tx *gorm.DB) {
		if isTplModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// seedProvider inserts one provider row directly (the secret field holds a
// placeholder; the API only returns the masked view).
func seedProvider(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.Provider{Name: name, Type: "vultr", AccessKey: "v1:sealed", Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed provider %s: %v", name, err)
	}
	return row.ID
}

// itoaUint formats a row id for use in a path parameter.
func itoaUint(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// TestListProvidersFindFail a provider list read failure surfaces 500.
func TestListProvidersFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/providers", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateProviderErrors entry: bad bind, missing fields, explicit disable and
// persist failure.
func TestCreateProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	r, _ = newTestRouterWithMasterKey(t)
	for _, body := range []map[string]any{
		{"type": "vultr", "access_key": "sk"},
		{"name": "v", "access_key": "sk"},
		{"name": "v", "type": "vultr"},
	} {
		if rec := doJSON(t, r, http.MethodPost, "/api/providers", body); rec.Code != http.StatusBadRequest ||
			!strings.Contains(rec.Body.String(), "name, type and access_key are required") {
			t.Fatalf("missing fields: %d %s", rec.Code, rec.Body)
		}
	}

	// explicit disable persists (Enabled carries default:true, so false only
	// lands via an explicit write-back).
	r, db := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", map[string]any{
		"name": "off-p", "type": "vultr", "access_key": "sk-1", "enabled": false,
	}); rec.Code != http.StatusOK {
		t.Fatalf("disabled create: %d %s", rec.Code, rec.Body)
	}
	var row storage.Provider
	if err := db.Where("name = ?", "off-p").First(&row).Error; err != nil || row.Enabled {
		t.Fatalf("not persisted disabled: %+v err=%v", row, err)
	}

	// disable write-back failure 500.
	r, db = newTestRouterWithMasterKey(t)
	failProvidersUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", map[string]any{
		"name": "wb-fail", "type": "vultr", "access_key": "sk", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// persist failure 500.
	r, db = newTestRouterWithMasterKey(t)
	failProvidersCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", map[string]any{
		"name": "cf", "type": "vultr", "access_key": "sk",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateProviderErrors update: lookup 404 (bad id/not-found/DB failure share
// one convention), bad bind, type and switch branches, rotating the secret
// without a master key 500, save failure 500.
func TestUpdateProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "provider not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithMasterKey(t)
	id := seedProvider(t, db, "upd-p")
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/"+itoaUint(uint64(id)), "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// no such provider 404.
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/9999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// DB-failure lookup failure shares the convention, 404.
	r, db = newTestRouterWithMasterKey(t)
	seedProvider(t, db, "upd-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/1", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}

	// type and switch branches take effect.
	r, db = newTestRouterWithMasterKey(t)
	id = seedProvider(t, db, "upd-f")
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/"+itoaUint(uint64(id)),
		map[string]any{"type": "hetzner", "enabled": false, "name": "upd-f2"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var row storage.Provider
	if err := db.First(&row, id).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Type != "hetzner" || row.Enabled || row.Name != "upd-f2" {
		t.Fatalf("not persisted: %+v", row)
	}

	// rotating the secret on an instance without a master key 500 (internal
	// error, no details leaked).
	r2, db2 := newTestRouterWithDB(t)
	pid := seedProvider(t, db2, "noseal")
	if rec := doJSON(t, r2, http.MethodPut, "/api/providers/"+itoaUint(uint64(pid)),
		map[string]any{"access_key": "sk-new"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("seal: %d %s", rec.Code, rec.Body)
	}

	// save failure 500.
	r, db = newTestRouterWithMasterKey(t)
	id = seedProvider(t, db, "upd-save")
	failProvidersUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "x"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteProviderErrors delete: lookup 404, unreferenced delete succeeds, and
// delete-persist failure 500 (the 409 for templates blocking deletion is already
// covered by the existing TestProviderAPI).
func TestDeleteProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/providers/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// no template references: delete succeeds.
	r, db := newTestRouterWithMasterKey(t)
	id := seedProvider(t, db, "del-ok")
	if rec := doJSON(t, r, http.MethodDelete, "/api/providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.Provider{}).Count(&n)
	if n != 0 {
		t.Fatalf("providers left: %d", n)
	}

	// delete-persist failure 500.
	r, db = newTestRouterWithMasterKey(t)
	id = seedProvider(t, db, "del-fail")
	failProvidersDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateTemplateRules tests each template validation branch directly
// (including the provider_id/direction/config branches already exercised by the
// existing API tests, locking in the rule conventions as a whole).
func TestValidateTemplateRules(t *testing.T) {
	// Rebuild the input per case: provider-zero writes through the shared
	// pointer, so a baseline cannot be reused.
	fresh := func() templateInput {
		pid := uint(1)
		return templateInput{Name: "t", ProviderID: &pid, BillingType: "包月"}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*templateInput)
		msg    string
	}{
		{"name", func(in *templateInput) { in.Name = "" }, "name is required"},
		{"provider-nil", func(in *templateInput) { in.ProviderID = nil }, "provider_id is required"},
		{"provider-zero", func(in *templateInput) { *in.ProviderID = 0 }, "provider_id is required"},
		{"direction", func(in *templateInput) { in.Direction = "sideways" }, "direction must be out/in/both"},
		{"role", func(in *templateInput) { in.Role = "boss" }, "role must be entry/landing/both/relay"},
		{"billing", func(in *templateInput) { in.BillingType = "按年" }, "billing_type must be 包月/按流量"},
		{"config", func(in *templateInput) { in.Config = "{inbounds:" }, "config must be valid JSON"},
	} {
		in := fresh()
		tc.mutate(&in)
		if err := validateTemplate(in); err == nil || !strings.Contains(err.Error(), tc.msg) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.msg)
		}
	}
	if err := validateTemplate(fresh()); err != nil {
		t.Fatalf("valid base rejected: %v", err)
	}
}

// TestListTemplatesFindFail a template list read failure surfaces 500.
func TestListTemplatesFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/provision-templates", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateTemplateErrors creation: bad bind, role defaulting to entry, and
// persist failure.
func TestCreateTemplateErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	pid := seedProvider(t, db, "tpl-p")

	// bad bind 400.
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// create without a role: role defaults to entry (the direction default
	// branch is already exercised by the existing cases).
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "no-role", "provider_id": pid, "plan": "s-1", "region": "hkg", "billing_type": "按流量",
	}); rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var row storage.ProvisionTemplate
	if err := db.Where("name = ?", "no-role").First(&row).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if row.Role != "entry" || row.Direction != "out" {
		t.Fatalf("defaults: %+v", row)
	}

	// persist failure 500.
	r, db = newTestRouterWithDB(t)
	pid = seedProvider(t, db, "tpl-p2")
	failTplCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "cf", "provider_id": pid, "billing_type": "包月",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateTemplateErrors update: lookup 404, bad bind, validation failure,
// bandwidth and monthly-cost branches taking effect, save failure.
func TestUpdateTemplateErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/abc",
		map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "template not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	pid := seedProvider(t, db, "tpl-u")
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/9999",
		map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// DB-failure lookup failure shares the convention, 404.
	r, db = newTestRouterWithDB(t)
	seedProvider(t, db, "tpl-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1",
		map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	pid = seedProvider(t, db, "tpl-u2")
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "row-t", "provider_id": pid, "billing_type": "包月",
	}); rec.Code != http.StatusOK {
		t.Fatalf("seed tpl: %d %s", rec.Code, rec.Body)
	}

	// bad bind and validation failure 400.
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1",
		map[string]any{"provider_id": pid, "role": "boss"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	// bandwidth and monthly-cost branches take effect (the existing cases only
	// carried the traffic unit price).
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1", map[string]any{
		"name": "row-t2", "provider_id": pid, "billing_type": "包月",
		"bw_mbps": 300, "monthly_cost_cents": 900,
	}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var row storage.ProvisionTemplate
	if err := db.First(&row, 1).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.BwMbps != 300 || row.MonthlyCostCents != 900 || row.Name != "row-t2" {
		t.Fatalf("not persisted: %+v", row)
	}

	// save failure 500.
	r, db = newTestRouterWithDB(t)
	pid = seedProvider(t, db, "tpl-u3")
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "row-s", "provider_id": pid, "billing_type": "包月",
	}); rec.Code != http.StatusOK {
		t.Fatalf("seed tpl: %d %s", rec.Code, rec.Body)
	}
	failTplUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1",
		map[string]any{"name": "s2", "provider_id": pid, "billing_type": "包月"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteTemplateErrors delete: lookup 404 and delete-persist failure 500.
func TestDeleteTemplateErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/provision-templates/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	pid := seedProvider(t, db, "tpl-d")
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "row-d", "provider_id": pid, "billing_type": "包月",
	}); rec.Code != http.StatusOK {
		t.Fatalf("seed tpl: %d %s", rec.Code, rec.Body)
	}
	failTplDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/provision-templates/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}
