package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newTestRouter builds a gin test engine on a private temporary database.
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r, _ := newTestRouterWithDB(t)
	return r
}

// newTestRouterWithDB additionally returns the db handle, for cases that
// insert related data such as traffic directly.
func newTestRouterWithDB(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	r, db := newTestRouterCfg(t, nil)
	return r, db
}

// newTestRouterCfg layers case-specific customization onto the default config
// (backup directory, master key and other items that need temporary paths);
// everything else matches newTestRouterWithDB.
func newTestRouterCfg(t *testing.T, mutate func(*config.Config)) (*gin.Engine, *gorm.DB) {
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
	if mutate != nil {
		mutate(&cfg)
	}
	r, _ := NewRouter(db, cfg)
	return r, db
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	// the admin data plane has sat behind apiAuth since 2026-10-10 (security fix):
	// the helper attaches an admin JWT by default, and cases testing missing or
	// invalid credentials use doBare or build the request with their own Authorization.
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doBare sends a request with no credentials: for auth negative cases such as
// "no token should give 401".
func doBare(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doRaw sends a raw string body (for bad JSON negative cases, since doJSON
// can only send valid JSON); everything else matches doJSON and an admin JWT
// is attached by default.
func doRaw(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestNodeCRUD(t *testing.T) {
	r := newTestRouter(t)

	// create: returns 201 carrying the generated agent token
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-1", "address": "hk.example.com", "port": 443,
		"protocol": "vless", "config": `{"uuid":"u1","tls":true}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create resp: %v", err)
	}
	if created["token"].(string) == "" {
		t.Fatal("created node must carry agent token")
	}
	if created["status"] != "unknown" {
		t.Fatalf("initial status = %v", created["status"])
	}
	id := created["id"].(float64)

	// list and detail
	if rec = doJSON(t, r, "GET", "/api/nodes", nil); rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list len=%d err=%v", len(list), err)
	}

	// update: partial fields, unsent fields keep their values
	rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{"port": 8443, "enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body)
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["port"].(float64) != 8443 || updated["address"] != "hk.example.com" || updated["enabled"] != false {
		t.Fatalf("partial update lost fields: %v", updated)
	}
	_ = id

	// illegal protocol rejected (an unregistered name: wireguard is a registered
	// protocol since the pluginization refactor, only its render hooks are absent)
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "x", "address": "a", "port": 1, "protocol": "bogus",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad protocol status = %d", rec.Code)
	}

	// invalid JSON config rejected
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "x", "address": "a", "port": 1, "protocol": "vless", "config": "{bad",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad config status = %d", rec.Code)
	}

	// fetching after delete gives 404
	if rec = doJSON(t, r, "DELETE", "/api/nodes/1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d", rec.Code)
	}
}

func TestNodeMetaInput(t *testing.T) {
	r := newTestRouter(t)

	// create with metadata: stored as requested (E-24 panel-editable shape)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-entry", "address": "hk.example.com", "port": 443, "protocol": "vless",
		"role": "entry", "direction": "out", "region": "香港", "isp": "HKT", "line_type": "cn2_gia",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create meta status = %d body=%s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	for k, want := range map[string]any{
		"role": "entry", "direction": "out", "region": "香港", "isp": "HKT", "line_type": "cn2_gia",
	} {
		if created[k] != want {
			t.Fatalf("created[%s] = %v, want %v", k, created[k], want)
		}
	}

	// create without metadata: DB defaults land (subscription grouping relies on
	// non-empty values)
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "plain", "address": "a", "port": 1, "protocol": "vless",
	})
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["role"] != "landing" || created["region"] != "未知" || created["isp"] != "未知" {
		t.Fatalf("defaults = role:%v region:%v isp:%v", created["role"], created["region"], created["isp"])
	}

	// update: change only the region, the rest of the metadata stays
	rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{"region": "圣何塞"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update meta status = %d body=%s", rec.Code, rec.Body)
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["region"] != "圣何塞" || updated["role"] != "entry" || updated["isp"] != "HKT" {
		t.Fatalf("partial meta update lost fields: %v", updated)
	}

	// illegal enum values rejected
	for k, v := range map[string]string{"role": "boss", "direction": "up", "transport": "pigeon"} {
		rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{k: v})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad %s status = %d", k, rec.Code)
		}
	}
}

func TestNodeTokenRotation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	oldToken := "old-node-token"
	node := storage.Node{Name: "rotate", Address: "rotate.example.com", Port: 443,
		Protocol: "vless", Token: oldToken, Enabled: true, Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	rec := doJSON(t, r, "POST", "/api/nodes/1/token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	var rotated storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rotated.Token == "" {
		t.Fatalf("decode rotate: %v %q", err, rec.Body)
	}
	if rotated.OldToken != oldToken || rotated.Token == oldToken {
		t.Fatalf("rotation fields: old=%q new=%q", rotated.OldToken, rotated.Token)
	}
	if rotated.TokenRotatedAt == nil || time.Since(*rotated.TokenRotatedAt) > time.Minute {
		t.Fatalf("rotated at = %v", rotated.TokenRotatedAt)
	}

	var row storage.Node
	if err := db.First(&row, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Token != rotated.Token || row.OldToken != oldToken || row.TokenRotatedAt == nil {
		t.Fatalf("rotation not persisted: %+v", row)
	}
}

func TestNodeTokenRevocation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	oldToken := "grace-token"
	node := storage.Node{Name: "revoke", Address: "revoke.example.com", Port: 443,
		Protocol: "vless", Token: "current-token", OldToken: oldToken,
		TokenRotatedAt: ptrTime(time.Now()), Enabled: true, Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	if rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{"token": "current-token"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("current token: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{"token": oldToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}

	var revocations []storage.NodeTokenRevocation
	if err := db.Find(&revocations).Error; err != nil || len(revocations) != 1 || revocations[0].Token != oldToken {
		t.Fatalf("revocations = %+v err=%v", revocations, err)
	}
	var row storage.Node
	if err := db.First(&row, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.OldToken != "" || row.TokenRotatedAt != nil || row.Token != "current-token" {
		t.Fatalf("grace not closed: %+v", row)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

// TestNodeSchedPriority covers the scheduling priority tier API (F-1029):
// set at create, updated with nil-keeps / explicit-0-resets semantics,
// negative values rejected, and the tier feeds allocation ordering.
func TestNodeSchedPriority(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// create with a tier
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "pr-1", "address": "pr1.example.com", "port": 443,
		"protocol": "vless", "sched_priority": 3,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var created storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.SchedPriority != 3 {
		t.Fatalf("created sched_priority = %d", created.SchedPriority)
	}

	// update with an explicit 0 resets; a payload without the field keeps it
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"sched_priority": 0,
	}); rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d body=%s", rec.Code, rec.Body)
	}
	var row storage.Node
	if err := db.First(&row, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.SchedPriority != 0 {
		t.Fatalf("explicit 0 reset failed: %+v", row)
	}
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"sched_priority": 7,
	}); rec.Code != http.StatusOK {
		t.Fatalf("set status = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"line_type": "cn2_gia",
	}); rec.Code != http.StatusOK {
		t.Fatalf("keep status = %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.SchedPriority != 7 {
		t.Fatalf("absent field must keep the tier: %+v", row)
	}

	// negative values invert the contract
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"sched_priority": -1,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative status = %d %s", rec.Code, rec.Body)
	}
}

// TestNodePlanID covers the plan-binding API (F-1047): set at create,
// nil-keeps / explicit-0-clears on update, and the value lands on the node
// the sweep's per-plan mode resolution reads.
func TestNodePlanID(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "plan-1", "address": "plan1.example.com", "port": 443,
		"protocol": "vless", "plan_id": 5,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var created storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.PlanID != 5 {
		t.Fatalf("created plan_id = %d", created.PlanID)
	}

	// an absent field keeps the binding
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"line_type": "cn2_gia",
	}); rec.Code != http.StatusOK {
		t.Fatalf("keep status = %d %s", rec.Code, rec.Body)
	}
	var row storage.Node
	if err := db.First(&row, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.PlanID != 5 {
		t.Fatalf("absent plan_id must keep: %+v", row)
	}

	// an explicit 0 clears the binding
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(created.ID), map[string]any{
		"plan_id": 0,
	}); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&row, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.PlanID != 0 {
		t.Fatalf("explicit 0 clear failed: %+v", row)
	}
}
