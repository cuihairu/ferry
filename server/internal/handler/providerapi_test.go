package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newTestRouterWithMasterKey builds a test router with a secret master key (for
// OS-1 credential entry), returning a separate store with the same master key to
// verify the ciphertext.
func newTestRouterWithMasterKey(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	cfg.SecretKey = "test-master-key"
	r, _ := NewRouter(db, cfg)
	return r, db
}

// TestProviderAPI covers provider credentials (OS-1): entry is encrypted before
// hitting the database (no plaintext stored), the list does not echo the key
// (has_access_key), secret rotation, an empty secret being preserved, providers
// with templates refusing deletion, and instances without a master key refusing
// credential entry.
func TestProviderAPI(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")

	// enter credentials
	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "vultr-main", "type": "vultr", "access_key": "sk-live-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create provider status = %d body=%s", rec.Code, rec.Body)
	}
	var pv struct {
		ID           uint   `json:"id"`
		HasAccessKey bool   `json:"has_access_key"`
		AccessKey    string `json:"access_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if !pv.HasAccessKey || pv.AccessKey != "" {
		t.Fatalf("provider view leaks key: %+v", pv)
	}

	// stored encrypted and decryptable back to plaintext
	var row storage.Provider
	if err := db.First(&row, pv.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.AccessKey == "sk-live-secret" {
		t.Fatal("access key must be stored encrypted")
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-live-secret" {
		t.Fatalf("decrypt stored key = %q err=%v", plain, err)
	}

	// list masking: only has_access_key, no plaintext
	rec = doJSON(t, r, "GET", "/api/providers", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if !json.Valid([]byte(body)) || !containsStr(body, `"has_access_key":true`) || containsStr(body, "sk-live-secret") {
		t.Fatalf("list leaks key: %s", body)
	}

	// rotate the secret
	id := strconv.FormatUint(uint64(pv.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"access_key": "sk-new"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update provider status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatalf("rotated key = %q err=%v", plain, err)
	}

	// empty secret preserved: rename only
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"name": "vultr-renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if row.Name != "vultr-renamed" {
		t.Fatalf("name = %s", row.Name)
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatal("rename must keep access key")
	}

	// deletion refused while a template references it
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": pv.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "direction": "out", "line_type": "163", "role": "entry",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/providers/"+id, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete provider with templates status = %d", rec.Code)
	}

	// an instance without a master key refuses entry (no plaintext in the DB)
	r2, _ := newTestRouterWithDB(t)
	rec = doJSON(t, r2, "POST", "/api/providers", map[string]any{
		"name": "x", "type": "vultr", "access_key": "sk",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create without master key status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestTemplateAPI covers machine templates (OS-1): CRUD and field validation.
func TestTemplateAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: "v1:a:b", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}

	// invalid direction refused
	rec := doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "bad", "provider_id": prov.ID, "direction": "sideways",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid direction status = %d", rec.Code)
	}
	// missing provider refused
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{"name": "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider status = %d", rec.Code)
	}
	// invalid JSON in the config template refused (OS-4 lands a node row per
	// provisioned server at open; an invalid config would be pointless)
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "bad", "provider_id": prov.ID, "config": "{inbounds:",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid config status = %d", rec.Code)
	}

	// create a template (defaults direction=out role=entry; the config template
	// is stored compacted)
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": prov.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "monthly_cost_cents": 500,
		"traffic_price_cents": 0, "line_type": "163", "role": "entry", "transport": "ws-tls",
		"config": `{ "inbounds": [] }`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	var tpl struct {
		ID               uint   `json:"id"`
		Name             string `json:"name"`
		Direction        string `json:"direction"`
		Role             string `json:"role"`
		BillingType      string `json:"billing_type"`
		MonthlyCostCents int64  `json:"monthly_cost_cents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tpl); err != nil {
		t.Fatal(err)
	}
	if tpl.Direction != "out" || tpl.Role != "entry" || tpl.BillingType != "包月" || tpl.MonthlyCostCents != 500 {
		t.Fatalf("template = %+v", tpl)
	}
	var row storage.ProvisionTemplate
	if err := db.First(&row, tpl.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Config != `{"inbounds":[]}` {
		t.Fatalf("template config must be compacted: %q", row.Config)
	}

	// update and delete
	tid := strconv.FormatUint(uint64(tpl.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/provision-templates/"+tid, map[string]any{
		"name": "hk-3t-v2", "provider_id": prov.ID, "plan": "vc2-2c-2gb", "region": "hkg",
		"billing_type": "按流量", "traffic_price_cents": 30, "direction": "in", "line_type": "cn2_gia", "role": "both",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/provision-templates", nil)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), "hk-3t-v2") {
		t.Fatalf("list templates = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/provision-templates/"+tid, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete template status = %d", rec.Code)
	}
	var n int64
	db.Model(&storage.ProvisionTemplate{}).Count(&n)
	if n != 0 {
		t.Fatalf("templates after delete = %d", n)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestProviderNoteRegion covers F-0323 (account remark) and F-0324 (account
// scope): both are set at create, echoed in views, and updatable without
// clearing the credentials.
func TestProviderNoteRegion(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)

	note := "team E edge account"
	region := "fra"
	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "vultr-fra", "type": "vultr", "access_key": "sk-secret",
		"note": note, "region": region,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var pv struct {
		ID     uint   `json:"id"`
		Note   string `json:"note"`
		Region string `json:"region"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if pv.Note != note || pv.Region != region {
		t.Fatalf("create view = %+v", pv)
	}

	// Update: change the note and region, keep the credentials untouched
	// (no access_key in the payload).
	newNote := "renewed Q3"
	newRegion := "nyc1"
	rec = doJSON(t, r, "PUT", "/api/providers/"+strconv.Itoa(int(pv.ID)), map[string]any{
		"note": newNote, "region": newRegion,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body)
	}
	pv = struct {
		ID     uint   `json:"id"`
		Note   string `json:"note"`
		Region string `json:"region"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if pv.Note != newNote || pv.Region != newRegion {
		t.Fatalf("update view = %+v", pv)
	}
	list := doJSON(t, r, "GET", "/api/providers", nil)
	if list.Code != http.StatusOK || !containsStr(list.Body.String(), newNote) ||
		!containsStr(list.Body.String(), newRegion) {
		t.Fatalf("list = %d %s", list.Code, list.Body)
	}
}

// TestProviderTestConnection covers F-0328: the endpoint verifies the stored
// credential locally for types without an adapter, probes the vendor through
// a registered cloudprov adapter otherwise, and reports typed failures.
func TestProviderTestConnection(t *testing.T) {
	r, _ := newTestRouterWithMasterKey(t)

	// Unknown id: 404.
	if rec := doJSON(t, r, "POST", "/api/providers/999/test", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider = %d", rec.Code)
	}

	// A type without a registered adapter: local credential check only.
	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "tofu-only", "type": "tofu-cli", "access_key": "sk-local",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s", rec.Code, rec.Body)
	}
	body := mustJSON(t, rec)
	if body["ok"] != true || body["mode"] != "local" {
		t.Fatalf("local test = %v", body)
	}

	// A registered adapter: the probe goes to the vendor (fake here).
	cloudprov.Register("fake-probe", func(a cloudprov.Account) (cloudprov.Adapter, error) {
		f := cloudprov.NewFake()
		// The account flows through with its name and scope intact.
		if a.AccessKey == "" {
			return nil, errors.New("credential not passed to adapter")
		}
		f.Seed(cloudprov.Resource{ResourceID: "r-1", Name: a.Name, Region: a.Region})
		return f, nil
	})
	t.Cleanup(func() { cloudprov.Unregister("fake-probe") })

	rec = doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "fake-acct", "type": "fake-probe", "access_key": "sk-fake", "region": "fra",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create fake = %d %s", rec.Code, rec.Body)
	}
	fakeID := uint(mustJSON(t, rec)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(fakeID)+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("adapter test = %d %s", rec.Code, rec.Body)
	}
	body = mustJSON(t, rec)
	if body["ok"] != true || body["mode"] != "adapter" || body["api_version"] != "2.1" {
		t.Fatalf("adapter probe = %v", body)
	}
	if body["capabilities"] == nil {
		t.Fatal("capabilities missing from probe result")
	}

	// An adapter that rejects the credential: typed auth failure surfaces.
	cloudprov.Register("fake-bad", func(a cloudprov.Account) (cloudprov.Adapter, error) {
		return nil, cloudprov.NewError(cloudprov.ErrAuth, cloudprov.OpCapabilities, "", "401", "bad key")
	})
	t.Cleanup(func() { cloudprov.Unregister("fake-bad") })
	rec = doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "bad-acct", "type": "fake-bad", "access_key": "sk-bad",
	})
	badID := uint(mustJSON(t, rec)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(badID)+"/test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bad test = %d %s", rec.Code, rec.Body)
	}
	body = mustJSON(t, rec)
	if body["ok"] != false || body["error_type"] != "auth" {
		t.Fatalf("bad probe = %v", body)
	}

	// A disabled account refuses the test outright.
	rec = doJSON(t, r, "PUT", "/api/providers/"+utoa(id), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/test", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("disabled test = %d %s", rec.Code, rec.Body)
	}
}

// TestProviderCredentialRotation covers F-0330: rotation keeps the previous
// key through the grace window, bumps the version, and an explicit revoke
// drops it; a plain credential replace keeps no grace copy.
func TestProviderCredentialRotation(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")

	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "rot-acct", "type": "vultr", "access_key": "sk-v1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	body := mustJSON(t, rec)
	id := uint(body["id"].(float64))
	if body["key_version"].(float64) != 1 || body["prev_key_held"] != false {
		t.Fatalf("initial view = %v", body)
	}

	// Rotate: v2 becomes active, v1 is held in the grace window.
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/rotate", map[string]any{
		"access_key": "sk-v2",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	body = mustJSON(t, rec)
	if body["key_version"].(float64) != 2 || body["prev_key_held"] != true || body["prev_set_at"] == nil {
		t.Fatalf("rotated view = %v", body)
	}
	var row storage.Provider
	if err := db.First(&row, id).Error; err != nil {
		t.Fatal(err)
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-v2" {
		t.Fatalf("active key = %q err=%v", plain, err)
	}
	if plain, err := store.Decrypt(row.PrevAccessKey); err != nil || plain != "sk-v1" {
		t.Fatalf("grace key = %q err=%v", plain, err)
	}
	// The view never leaks either ciphertext.
	if raw := rec.Body.String(); strings.Contains(raw, row.AccessKey) || strings.Contains(raw, row.PrevAccessKey) {
		t.Fatalf("rotation view leaks ciphertext: %s", raw)
	}

	// Rotate requires a key.
	if rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/rotate", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty rotate = %d", rec.Code)
	}

	// Early revoke drops the grace copy.
	rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/revoke-previous", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/providers/"+utoa(id)+"/revoke-previous", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("double revoke = %d", rec.Code)
	}

	// Plain replace keeps no grace copy but still bumps the version.
	rec = doJSON(t, r, "PUT", "/api/providers/"+utoa(id), map[string]any{"access_key": "sk-v3"})
	if rec.Code != http.StatusOK {
		t.Fatalf("replace = %d %s", rec.Code, rec.Body)
	}
	body = mustJSON(t, rec)
	if body["key_version"].(float64) != 3 || body["prev_key_held"] != false {
		t.Fatalf("replace view = %v", body)
	}
}
