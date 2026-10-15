package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十九批补口：节点 CRUD 各 400/404/500 口径——列表与单读取数失败、建号
// 坏绑定/缺名/缺地址/端口越界/hysteria2 配置非对象、显式停用与补写失败、
// 落库失败、更新库故障定位 500/坏绑定/回写失败/回落再查失败/全元数据回写、
// 删除查无 404/落删失败、normalizeConfig 与 parseNodeConfig 直测边界。
// 留白：RandomToken 的 crypto/rand 错误（正常流程不可达）与 normalizeConfig
// 的 Marshal 防御分支（JSON 反解值再序列化恒可成功）不硬凑。

// failNodeQueriesAt 第 n 次（1 起）针对节点表的查询失败：更新后回落再查用
// 次序定向。
func failNodeQueriesAt(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:node_query_at", func(tx *gorm.DB) {
		if !isNodeModel(tx.Statement.Model) {
			return
		}
		seen++
		if seen == n {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNodeCreates 让节点行创建失败。
func failNodeCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:node_create", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNodeDeletes 让节点行删除失败。
func failNodeDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:node_delete", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// TestListNodeErrors 列表与单读的取数失败口径（定位失败同 404 口径已在
// 既有用例覆盖，此处补库故障 500）。
func TestListNodeErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodGet, "/api/nodes", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/nodes/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
}

// TestCreateNodeErrors 建号：坏绑定与校验面、hysteria2 配置非对象、元数据
// 接管、显式停用与补写失败、落库失败。
func TestCreateNodeErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/nodes", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		name string
		body map[string]any
		msg  string
	}{
		{"name", map[string]any{"address": "a.example.com", "port": 443, "protocol": "vless"}, "name is required"},
		{"address", map[string]any{"name": "n", "port": 443, "protocol": "vless"}, "address is required"},
		{"port", map[string]any{"name": "n", "address": "a.example.com", "protocol": "vless"}, "port must be 1-65535"},
		{"hysteria2-config", map[string]any{"name": "n", "address": "a.example.com", "port": 443,
			"protocol": "hysteria2", "config": "[1]"}, "config must be a JSON object"},
	} {
		if rec := doJSON(t, r, http.MethodPost, "/api/nodes", tc.body); rec.Code != http.StatusBadRequest ||
			!containsStr(rec.Body.String(), tc.msg) {
			t.Fatalf("%s: %d %s", tc.name, rec.Code, rec.Body)
		}
	}

	// 元数据齐带：MetaInit 接管 + 各字段落库。
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "meta-full", "address": "m.example.com", "port": 443, "protocol": "vless",
		"role": "landing", "direction": "in", "line_type": "cn2_gia", "region": "华东",
		"city": "上海", "datacenter": "dc-1", "isp": "电信", "transport": "quic",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("meta create: %d %s", rec.Code, rec.Body)
	}
	var n storage.Node
	if err := db.Where("name = ?", "meta-full").First(&n).Error; err != nil {
		t.Fatalf("find: %v", err)
	}
	if !n.MetaInit || n.Role != "landing" || n.Direction != "in" || n.LineType != "cn2_gia" ||
		n.Region != "华东" || n.City != "上海" || n.Datacenter != "dc-1" || n.ISP != "电信" ||
		n.Transport != "quic" {
		t.Fatalf("meta not persisted: %+v", n)
	}

	// 显式停用入库（Enabled 带 default:true，需显式补写才落 false）。
	rec = doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "off-n", "address": "o.example.com", "port": 443, "protocol": "vless", "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("disabled create: %d %s", rec.Code, rec.Body)
	}
	// 注意用全新结构体查询：First 会把 dest 结构体既有的主键并进条件。
	var off storage.Node
	if err := db.Where("name = ?", "off-n").First(&off).Error; err != nil || off.Enabled {
		t.Fatalf("not persisted disabled: %+v err=%v", off, err)
	}

	// 停用补写失败 500。
	r, db = newTestRouterWithDB(t)
	failNodeUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "wb-fail", "address": "w.example.com", "port": 443, "protocol": "vless", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// 落库失败 500。
	r, db = newTestRouterWithDB(t)
	failNodeCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "cf-n", "address": "c.example.com", "port": 443, "protocol": "vless",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateNodeErrors 更新：库故障定位 500、坏绑定、全元数据回写、回写
// 失败与回落再查失败、删除查无 404 与落删失败。
func TestUpdateNodeErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedNodeRow(t, db, "upd-n")
	closeSQL(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{"port": 443}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locate: %d %s", rec.Code, rec.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "bind-n")
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}

	// 全元数据回写（metaUpdates 八分支）+ 基础字段。
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "meta-n")
	rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{
		"port": 8443, "role": "entry", "direction": "both", "line_type": "iplc",
		"region": "华南", "city": "广州", "datacenter": "dc-9", "isp": "移动", "transport": "ssh",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("meta update: %d %s", rec.Code, rec.Body)
	}
	var n storage.Node
	if err := db.First(&n, 1).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !n.MetaInit || n.Role != "entry" || n.Direction != "both" || n.LineType != "iplc" ||
		n.Region != "华南" || n.City != "广州" || n.Datacenter != "dc-9" || n.ISP != "移动" ||
		n.Transport != "ssh" || n.Port != 8443 {
		t.Fatalf("meta not updated: %+v", n)
	}

	// 回写失败 500。
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "upd-fail")
	failNodeUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{"port": 443}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}

	// 回落再查失败 500（定位 1 → 更新 → 复查 2）。
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "upd-refind")
	failNodeQueriesAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{"port": 443}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}

	// 删除：查无 404 与落删失败 500。
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "del-n")
	if rec := doJSON(t, r, http.MethodDelete, "/api/nodes/9999", nil); rec.Code != http.StatusNotFound ||
		!containsStr(rec.Body.String(), "node not found") {
		t.Fatalf("delete missing: %d %s", rec.Code, rec.Body)
	}
	failNodeDeletes(t, db)
	if rec := doJSON(t, r, http.MethodDelete, "/api/nodes/1", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete fail: %d %s", rec.Code, rec.Body)
	}
}

// seedNodeRow 直落一行节点。
func seedNodeRow(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.Node{Name: name, Address: name + ".example.com", Port: 443,
		Protocol: "vless", Token: "tok-" + name, Enabled: true, Status: "unknown"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	return row.ID
}

// TestConfigNormalizers 配置归一与解析的直测边界：空值回 {}、非法 JSON
// 原样保留（防御）、hysteria2 密码校验与坏 JSON 拦截。
func TestConfigNormalizers(t *testing.T) {
	if got := normalizeConfig(""); got != "{}" {
		t.Fatalf("empty: %q", got)
	}
	if got := normalizeConfig("   "); got != "{}" {
		t.Fatalf("blank: %q", got)
	}
	if got := normalizeConfig(`{ "a" : 1 }`); got != `{"a":1}` {
		t.Fatalf("compact: %q", got)
	}
	if got := normalizeConfig("bad{"); got != "bad{" {
		t.Fatalf("invalid passthrough: %q", got)
	}

	// parseNodeConfig：空与 {} 返回零值，非对象报错。
	if out, err := parseNodeConfig(""); err != nil || out.Password != "" {
		t.Fatalf("empty: %+v err=%v", out, err)
	}
	if out, err := parseNodeConfig("{}"); err != nil || out.Password != "" {
		t.Fatalf("empty obj: %+v err=%v", out, err)
	}
	if _, err := parseNodeConfig("[1]"); err == nil || !containsStr(err.Error(), "config must be a JSON object") {
		t.Fatalf("array: err=%v", err)
	}

	// validateNodeConfig 的 hysteria2 分支：缺密码与非对象配置各自拦截。
	in := model.NodeInput{Protocol: model.ProtoHysteria2, Port: 443, Config: `{"password":"p"}`}
	if err := validateNodeConfig(&in); err != nil {
		t.Fatalf("valid hysteria2: %v", err)
	}
	in.Config = `{}`
	if err := validateNodeConfig(&in); err == nil || !containsStr(err.Error(), "config.password") {
		t.Fatalf("missing password: %v", err)
	}
	in.Config = `[1]`
	if err := validateNodeConfig(&in); err == nil || !containsStr(err.Error(), "config must be a JSON object") {
		t.Fatalf("bad config: %v", err)
	}
}
