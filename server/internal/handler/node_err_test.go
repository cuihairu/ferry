package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Batch 29 gap fill: the 400/404/500 shapes of node CRUD -- list and single
// read fetch failures, create bad bind / missing name / missing address / port
// out of range / non-object hysteria2 config, explicit disable and write-back
// failure, persist failure, update locate 500 on DB fault / bad bind /
// write-back failure / re-find failure / full metadata write-back, delete
// missing 404 and delete failure, plus direct edge cases of normalizeConfig
// and parseNodeConfig.
// Left blank: the crypto/rand error of RandomToken (unreachable in normal flow)
// and the defensive Marshal branch of normalizeConfig (re-serializing a JSON
// decoded value always succeeds) are not forced.

// failNodeQueriesAt makes the nth (1-based) query against the node table fail;
// use the ordering to target the post-update re-find.
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

// failNodeCreates makes node row creation fail.
func failNodeCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:node_create", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// failNodeDeletes makes node row deletion fail.
func failNodeDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Delete().Before("gorm:delete").Register("test:node_delete", func(tx *gorm.DB) {
		if isNodeModel(tx.Statement.Model) {
			tx.AddError(errors.New("db down"))
		}
	})
}

// TestListNodeErrors covers the fetch-failure shapes of list and single read
// (the locate failure mapping to 404 is already covered by an existing case;
// this adds the 500 on DB fault).
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

// TestCreateNodeErrors create: bad bind and the validation facets, non-object
// hysteria2 config, metadata takeover, explicit disable and write-back failure,
// persist failure.
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

	// full metadata supplied: MetaInit takeover plus each field persisted.
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

	// explicit disable persisted (Enabled carries default:true, so false only
	// lands through the explicit write-back).
	rec = doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "off-n", "address": "o.example.com", "port": 443, "protocol": "vless", "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("disabled create: %d %s", rec.Code, rec.Body)
	}
	// query with a fresh struct: First folds any primary key already set on the
	// dest struct into the conditions.
	var off storage.Node
	if err := db.Where("name = ?", "off-n").First(&off).Error; err != nil || off.Enabled {
		t.Fatalf("not persisted disabled: %+v err=%v", off, err)
	}

	// disable write-back failure 500.
	r, db = newTestRouterWithDB(t)
	failNodeUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "wb-fail", "address": "w.example.com", "port": 443, "protocol": "vless", "enabled": false,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("write-back: %d %s", rec.Code, rec.Body)
	}

	// persist failure 500.
	r, db = newTestRouterWithDB(t)
	failNodeCreates(t, db)
	if rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "cf-n", "address": "c.example.com", "port": 443, "protocol": "vless",
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestUpdateNodeErrors update: locate 500 on DB fault, bad bind, full
// metadata write-back, write-back failure and re-find failure, delete missing
// 404 and delete failure.
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

	// full metadata write-back (all eight metaUpdates branches) plus base fields.
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

	// write-back failure 500.
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "upd-fail")
	failNodeUpdates(t, db)
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{"port": 443}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}

	// re-find failure 500 (locate is query 1 -> update -> re-find is query 2).
	r, db = newTestRouterWithDB(t)
	seedNodeRow(t, db, "upd-refind")
	failNodeQueriesAt(t, db, 2)
	if rec := doJSON(t, r, http.MethodPut, "/api/nodes/1", map[string]any{"port": 443}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("re-find: %d %s", rec.Code, rec.Body)
	}

	// delete: missing 404 and delete failure 500.
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

// seedNodeRow inserts one node row directly.
func seedNodeRow(t *testing.T, db *gorm.DB, name string) uint {
	t.Helper()
	row := storage.Node{Name: name, Address: name + ".example.com", Port: 443,
		Protocol: "vless", Token: "tok-" + name, Enabled: true, Status: "unknown"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed node %s: %v", name, err)
	}
	return row.ID
}

// TestConfigNormalizers direct edge cases of config normalization and parsing:
// empty falls back to {}, invalid JSON is kept as-is (defensive), the
// hysteria2 password check and bad JSON blocking.
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

	// parseNodeConfig: empty and {} return zero values, a non-object errors.
	if out, err := parseNodeConfig(""); err != nil || out.Password != "" {
		t.Fatalf("empty: %+v err=%v", out, err)
	}
	if out, err := parseNodeConfig("{}"); err != nil || out.Password != "" {
		t.Fatalf("empty obj: %+v err=%v", out, err)
	}
	if _, err := parseNodeConfig("[1]"); err == nil || !containsStr(err.Error(), "config must be a JSON object") {
		t.Fatalf("array: err=%v", err)
	}

	// the hysteria2 branch of validateNodeConfig: missing password and non-object
	// config are each blocked.
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
