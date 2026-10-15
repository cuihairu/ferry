package handler

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十八批补口：域名前置（BR-2）各 400/404/500 口径——DNS 商校验缺名缺型、
// 列表取数失败、录入坏绑定/缺 api_key/无主密钥/显式停用/落库失败、更新坏
// id/查无/库故障 404/坏绑定/未知类型/无主密钥换密/保存失败、删除坏 id/
// 无引用成功/落删失败、前置校验七规则直测、列表取数失败、建档坏绑定/
// 查无 DNS 商/落库失败、改档坏 id/查无/坏绑定/校验/查无 DNS 商/保存失败、
// 删档坏 id/查无/落删失败、geo-sync 对账库故障 500。
// 留白：无——通道行为外呼由 geodns 包假通道覆盖，不在本批范围。

// isDNSProviderModel 判断语句是否落在 DNS 商表上（Find 链 Model 是切片指针）。
func isDNSProviderModel(v any) bool {
	switch v.(type) {
	case *storage.DNSProvider, []storage.DNSProvider, *[]storage.DNSProvider:
		return true
	}
	return false
}

// isDNSFrontModel 判断语句是否落在前置记录表上。
func isDNSFrontModel(v any) bool {
	switch v.(type) {
	case *storage.DNSFront, []storage.DNSFront, *[]storage.DNSFront:
		return true
	}
	return false
}

// failDNSProviderCreates / failDNSProviderUpdates / failDNSProviderDeletes 让
// DNS 商行的建/改/删语句失败。
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

// failDNSFrontCreates / failDNSFrontUpdates / failDNSFrontDeletes 让前置行的
// 建/改/删语句失败。
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

// seedDNSProvider 直落一行 DNS 商（密文字段塞占位）。
func seedDNSProvider(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.DNSProvider{Name: name, Type: "cloudflare", APIKey: "sealed", Enabled: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed dns provider %s: %v", name, err)
	}
	return row.ID
}

// seedDNSFront 直落一行前置记录。
func seedDNSFront(t *testing.T, db *gorm.DB, providerID uint, name string) uint {
	t.Helper()
	row := storage.DNSFront{Name: name, Domain: name + ".example.com", ProviderID: providerID,
		PrimaryIP: "10.0.0.1", BackupIPs: `["10.0.0.2"]`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed dns front %s: %v", name, err)
	}
	return row.ID
}

// TestListDNSProvidersFindFail DNS 商列表取数失败冒 500。
func TestListDNSProvidersFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/dns-providers", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDNSProviderErrors 录入：坏绑定、缺名缺型、缺 api_key、无主密钥、
// 显式停用与落库失败。
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

	// api_key 缺失（校验过名与型后才轮到）。
	r, db := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "api_key is required") {
		t.Fatalf("api_key: %d %s", rec.Code, rec.Body)
	}

	// 无主密钥 500（禁止明文落库）。
	r2, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r2, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare", "api_key": "k"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("no master key: %d %s", rec.Code, rec.Body)
	}

	// 显式停用入库（Enabled 带 default:true，需显式补写才落 false）。
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers", map[string]any{
		"name": "cf-off", "type": "cloudflare", "api_key": "k", "enabled": false,
	}); rec.Code != http.StatusOK {
		t.Fatalf("disabled create: %d %s", rec.Code, rec.Body)
	}
	var row storage.DNSProvider
	if err := db.Where("name = ?", "cf-off").First(&row).Error; err != nil || row.Enabled {
		t.Fatalf("not persisted disabled: %+v err=%v", row, err)
	}

	// 停用补写失败 500。
	r, db = newTestRouterWithMasterKey(t)
	failDNSProviderUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers", map[string]any{
		"name": "wb-fail", "type": "cloudflare", "api_key": "k", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// 落库失败 500。
	r, db = newTestRouterWithMasterKey(t)
	failDNSProviderCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-providers",
		map[string]any{"name": "cf", "type": "cloudflare", "api_key": "k"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDNSProviderErrors 更新：定位 404（坏 id/查无/库故障同口径）、
// 坏绑定、未知类型、无主密钥换密 500、保存失败 500。
func TestUpdateDNSProviderErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	id := seedDNSProvider(t, db, "upd-dp")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns provider not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 查无与库故障同口径 404。
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

	// 坏绑定与未知类型 400。
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "upd-bind")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)), "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "upd-bind", "type": "alidns"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("validate: %d %s", rec.Code, rec.Body)
	}

	// 无主密钥换密 500（校验先过：名与型必带）。
	r2, db2 := newTestRouterWithDB(t)
	pid := seedDNSProvider(t, db2, "noseal")
	if rec := doJSON(t, r2, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(pid)),
		map[string]any{"name": "noseal", "type": "cloudflare", "api_key": "k-new"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("seal: %d %s", rec.Code, rec.Body)
	}

	// 保存失败 500。
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "upd-save")
	failDNSProviderUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-providers/"+itoaUint(uint64(id)),
		map[string]any{"name": "upd-save2", "type": "cloudflare"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteDNSProviderErrors 删除：坏 id 404、无引用成功与落删失败 500
// （有前置记录拒删的 409 已在既有 TestDNSAPI 覆盖）。
func TestDeleteDNSProviderErrors(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-providers/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 无引用成功删除。
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

	// 落删失败 500。
	r, db = newTestRouterWithMasterKey(t)
	id = seedDNSProvider(t, db, "del-fail")
	failDNSProviderDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-providers/"+itoaUint(uint64(id)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestValidateDNSFrontRules 前置校验七规则逐分支直测（每用例重建入参防
// 共享指针写穿）。
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

// TestListDNSFrontsFindFail 前置列表取数失败冒 500。
func TestListDNSFrontsFindFail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/dns-fronts", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateDNSFrontErrors 建档：坏绑定、查无 DNS 商与落库失败。
func TestCreateDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// 查无 DNS 商（校验已过才落定位）。
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", map[string]any{
		"name": "n", "domain": "edge.example.com", "provider_id": 9999,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`,
	}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "dns provider not found") {
		t.Fatalf("provider: %d %s", rec.Code, rec.Body)
	}

	// 落库失败 500。
	pid := seedDNSProvider(t, db, "cf-create")
	failDNSFrontCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts", map[string]any{
		"name": "n", "domain": "edge.example.com", "provider_id": pid,
		"primary_ip": "10.0.0.1", "backup_ips": `["10.0.0.2"]`,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateDNSFrontErrors 改档：定位 404、坏绑定、校验失败、查无 DNS 商
// 与保存失败。
func TestUpdateDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	pid := seedDNSProvider(t, db, "cf-upd")
	fid := seedDNSFront(t, db, pid, "upd-f")
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/abc", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns front not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 查无 404。
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/9999", map[string]any{"name": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// 坏绑定、校验失败与查无 DNS 商 400。
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

	// 保存失败 500。
	r, db = newTestRouterWithMasterKey(t)
	pid = seedDNSProvider(t, db, "cf-upd3")
	fid = seedDNSFront(t, db, pid, "upd-f3")
	failDNSFrontUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/dns-fronts/"+itoaUint(uint64(fid)), body); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestDeleteDNSFrontErrors 删档：坏 id 与查无 404、落删失败 500（切离中
// 拒删的 409 已在既有 TestDNSAPI 覆盖）。
func TestDeleteDNSFrontErrors(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	pid := seedDNSProvider(t, db, "cf-del")
	fid := seedDNSFront(t, db, pid, "del-f")
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/abc", nil); rec.Code != http.StatusNotFound ||
		!strings.Contains(rec.Body.String(), "dns front not found") {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	// 查无 404。
	r, _ = newTestRouterWithMasterKey(t)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d %s", rec.Code, rec.Body)
	}

	// 落删失败 500。
	r, db = newTestRouterWithMasterKey(t)
	pid = seedDNSProvider(t, db, "cf-del2")
	fid = seedDNSFront(t, db, pid, "del-f2")
	failDNSFrontDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/dns-fronts/"+itoaUint(uint64(fid)), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// TestGeoSyncDBError 手动对账的库故障：首段查询失败即冒 500。
func TestGeoSyncDBError(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/dns-fronts/geo-sync", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("db down: %d %s", rec.Code, rec.Body)
	}
}
