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

// 二十七批补口：供给配置（OS-1）各 400/404/500 口径——提供商列表取数失败、
// 录入坏绑定/缺字段/显式停用/落库失败、更新定位 404（坏 id 与查无与库故障
// 同口径）/坏绑定/类型与开关分支/无主密钥换机密/保存失败、删除定位与落删
// 失败、模板校验规则、列表取数失败、建档坏绑定/角色缺省/落库失败、改档
// 定位 404/坏绑定/校验失败/带宽月费分支/保存失败、删档定位与落删失败。
// 留白：模板列表成功路径已在既有 TestTemplateAPI 覆盖，此处只补错误分支。

// isProviderModel 判断语句是否落在提供商表上（Find 链 Model 是切片指针）。
func isProviderModel(v any) bool {
	switch v.(type) {
	case *storage.Provider, []storage.Provider, *[]storage.Provider:
		return true
	}
	return false
}

// isTplModel 判断语句是否落在机型模板表上。
func isTplModel(v any) bool {
	switch v.(type) {
	case *storage.ProvisionTemplate, []storage.ProvisionTemplate, *[]storage.ProvisionTemplate:
		return true
	}
	return false
}

// failProvidersCreates / failProvidersUpdates / failProvidersDeletes 让提供商
// 行的建/改/删语句失败。
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

// failTplCreates / failTplUpdates / failTplDeletes 让模板行的建/改/删语句失败。
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

// seedProvider 直落一行提供商（密文字段塞占位，接口只回掩码视图）。
func seedProvider(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.Provider{Name: name, Type: "vultr", AccessKey: "v1:sealed", Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed provider %s: %v", name, err)
	}
	return row.ID
}

// itoaUint 格式化行 id 供路径参数拼用。
func itoaUint(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// TestListProvidersFindFail 提供商列表取数失败冒 500。
func TestListProvidersFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/providers", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateProviderErrors 录入：坏绑定、缺字段、显式停用与落库失败。
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

	// 显式停用入库（Enabled 带 default:true，需显式补写才落 false）。
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

	// 停用补写失败 500。
	r, db = newTestRouterWithMasterKey(t)
	failProvidersUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", map[string]any{
		"name": "wb-fail", "type": "vultr", "access_key": "sk", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// 落库失败 500。
	r, db = newTestRouterWithMasterKey(t)
	failProvidersCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/providers", map[string]any{
		"name": "cf", "type": "vultr", "access_key": "sk",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateProviderErrors 更新：定位 404（坏 id/查无/库故障同口径）、坏
// 绑定、类型与开关分支、无主密钥换机密 500、保存失败 500。
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

	// 查无此提供商 404。
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/9999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// 库故障定位失败同口径 404。
	r, db = newTestRouterWithMasterKey(t)
	seedProvider(t, db, "upd-db")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/1", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}

	// 类型与开关分支生效。
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

	// 无主密钥实例换机密 500（内部错误不外泄细节）。
	r2, db2 := newTestRouterWithDB(t)
	pid := seedProvider(t, db2, "noseal")
	if rec := doJSON(t, r2, http.MethodPut, "/api/providers/"+itoaUint(uint64(pid)),
		map[string]any{"access_key": "sk-new"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("seal: %d %s", rec.Code, rec.Body)
	}

	// 保存失败 500。
	r, db = newTestRouterWithMasterKey(t)
	id = seedProvider(t, db, "upd-save")
	failProvidersUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "x"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteProviderErrors 删除：定位 404、无引用成功删除与落删失败 500
// （有模板拒删的 409 已在既有 TestProviderAPI 覆盖）。
func TestDeleteProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/providers/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 无模板引用成功删除。
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

	// 落删失败 500。
	r, db = newTestRouterWithMasterKey(t)
	id = seedProvider(t, db, "del-fail")
	failProvidersDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateTemplateRules 模板校验规则逐分支直测（含既有 API 用例已过
// 的 provider_id/方向/配置三分支，规则口径一并锁定）。
func TestValidateTemplateRules(t *testing.T) {
	// 每个用例重建入参：provider-zero 会写穿共享指针，基线不能复用。
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

// TestListTemplatesFindFail 模板列表取数失败冒 500。
func TestListTemplatesFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/provision-templates", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateTemplateErrors 建档：坏绑定与角色缺省落 entry、落库失败。
func TestCreateTemplateErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	pid := seedProvider(t, db, "tpl-p")

	// 坏绑定 400。
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// 不带角色建档：缺省 role=entry（direction 缺省分支既有用例已过）。
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

	// 落库失败 500。
	r, db = newTestRouterWithDB(t)
	pid = seedProvider(t, db, "tpl-p2")
	failTplCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/provision-templates", map[string]any{
		"name": "cf", "provider_id": pid, "billing_type": "包月",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateTemplateErrors 改档：定位 404、坏绑定、校验失败、带宽与月费
// 分支生效、保存失败。
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

	// 库故障定位失败同口径 404。
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

	// 坏绑定与校验失败 400。
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/provision-templates/1",
		map[string]any{"provider_id": pid, "role": "boss"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	// 带宽与月费分支生效（既有用例只带过流量单价）。
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

	// 保存失败 500。
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

// TestDeleteTemplateErrors 删档：定位 404 与落删失败 500。
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
