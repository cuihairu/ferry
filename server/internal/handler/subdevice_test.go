package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// getSubAt 以指定 UA/来源地址/hwid 请求订阅端点（httptest 缺省 RemoteAddr
// 固定，P2-4 设备/IP 面测试用多来源地址模拟多设备）。hwid 空串=不带该头。
func getSubAt(t *testing.T, r *gin.Engine, path, ua, remoteAddr, hwid string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", ua)
	if hwid != "" {
		req.Header.Set("X-Client-Hwid", hwid)
	}
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func createLimitedUser(t *testing.T, r *gin.Engine, name string, deviceLimit, ipLimit int) (path string, id float64) {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": name, "device_limit": deviceLimit, "ip_limit": ipLimit,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return "/sub/" + u["sub_token"].(string), u["id"].(float64)
}

func TestSubscriptionDeviceLimit(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, _ := createLimitedUser(t, r, "dev", 2, 0)

	// 两台设备在册内。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.1:1111", "hwid-aaa"); rec.Code != http.StatusOK {
		t.Fatalf("device 1: %d %s", rec.Code, rec.Body)
	}
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.2:1111", "hwid-bbb"); rec.Code != http.StatusOK {
		t.Fatalf("device 2: %d %s", rec.Code, rec.Body)
	}
	// 第三台超限 403。
	rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.3:1111", "hwid-ccc")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("device 3 want 403: %d %s", rec.Code, rec.Body)
	}
	// 已在册设备不受收紧影响（老设备放行）。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.1:1111", "hwid-aaa"); rec.Code != http.StatusOK {
		t.Fatalf("known device: %d %s", rec.Code, rec.Body)
	}
	// 未带 hwid 的请求不受 hwid 面限制（客户端未上报，该面不生效）。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.9:1111", ""); rec.Code != http.StatusOK {
		t.Fatalf("no-hwid request: %d %s", rec.Code, rec.Body)
	}
	// 告警按用户按日去重：同日多次超限只落一条。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.4:1111", "hwid-ddd"); rec.Code != http.StatusForbidden {
		t.Fatalf("device 4 want 403: %d %s", rec.Code, rec.Body)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = 'device_limit'").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("device_limit events = %d, want 1", n)
	}
}

func TestSubscriptionIPLimit(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, _ := createLimitedUser(t, r, "ipuser", 0, 1)

	// 同一 IP 重复访问不受限。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.1:1000", ""); rec.Code != http.StatusOK {
		t.Fatalf("ip 1 first: %d %s", rec.Code, rec.Body)
	}
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.1:2000", ""); rec.Code != http.StatusOK {
		t.Fatalf("ip 1 again: %d %s", rec.Code, rec.Body)
	}
	// 新 IP 超限 403。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.2:1000", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("ip 2 want 403: %d %s", rec.Code, rec.Body)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = 'device_limit'").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("device_limit events = %d, want 1", n)
	}

	// 窗口外旧行不占名额：手工插一条 25h 前的 IP 行，新 IP 应放行（旧行顺带清理）。
	var uid float64
	if err := db.Model(&storage.User{}).Select("id").Where("username = 'ipuser'").Scan(&uid).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := db.Create(&storage.SubDevice{UserID: uint(uid), Kind: "ip", Key: "10.1.9.9",
		FirstSeen: old, LastSeen: old}).Error; err != nil {
		t.Fatal(err)
	}
	// 先清掉在册的 10.1.0.1 让名额腾出——直接删行（窗口测试只看旧行是否放行）。
	if err := db.Where("kind = 'ip' AND last_seen > ?", time.Now().Add(-time.Hour)).
		Delete(&storage.SubDevice{}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.3:1000", ""); rec.Code != http.StatusOK {
		t.Fatalf("ip after window prune: %d %s", rec.Code, rec.Body)
	}
	var left int64
	if err := db.Model(&storage.SubDevice{}).Where("kind = 'ip' AND user_id = ?", uint(uid)).Count(&left).Error; err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("ip rows after prune = %d, want 1", left)
	}
}

func TestSubDeviceResetTokenClears(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, id := createLimitedUser(t, r, "reset", 1, 1)

	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.2.0.1:1000", "hwid-xyz"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "POST", "/api/users/"+strconv.FormatUint(uint64(id), 10)+"/sub-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	var cnt int64
	if err := db.Model(&storage.SubDevice{}).Where("user_id = ?", uint(id)).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("sub_devices after reset = %d, want 0", cnt)
	}
	// 新令牌可用。
	var u map[string]any
	if err := db.Raw("SELECT sub_token FROM users WHERE id = ?", uint(id)).Scan(&u).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getSubAt(t, r, "/sub/"+u["sub_token"].(string), "v2rayNG/1.8", "10.2.0.2:1000", "hwid-xyz"); rec.Code != http.StatusOK {
		t.Fatalf("new token: %d %s", rec.Code, rec.Body)
	}
}

func TestSubDeviceLimitValidation(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bad", "device_limit": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("device_limit -1: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bad2", "ip_limit": 1001}); rec.Code != http.StatusBadRequest {
		t.Fatalf("ip_limit 1001: %d", rec.Code)
	}
	// 设备清单端点。
	path, id := createLimitedUser(t, r, "list", 0, 0)
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.3.0.1:1000", "hwid-q"); rec.Code != http.StatusOK {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "GET", "/api/users/"+strconv.FormatUint(uint64(id), 10)+"/devices", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("devices: %d %s", rec.Code, rec.Body)
	}
	var rows []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 2 { // ip + hwid 各一条
		t.Fatalf("devices rows = %d, want 2", len(rows))
	}
}
