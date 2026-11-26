package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// getSubAt requests the subscription endpoint with a given UA/remote addr/hwid
// (httptest defaults RemoteAddr to a fixed value; P2-4 device/IP-dimension tests
// simulate multiple devices via multiple remote addrs). Empty hwid = omit the header.
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

	// Two devices fit within the limit.
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.1:1111", "hwid-aaa"); rec.Code != http.StatusOK {
		t.Fatalf("device 1: %d %s", rec.Code, rec.Body)
	}
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.2:1111", "hwid-bbb"); rec.Code != http.StatusOK {
		t.Fatalf("device 2: %d %s", rec.Code, rec.Body)
	}
	// A third device exceeds the limit: 403.
	rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.3:1111", "hwid-ccc")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("device 3 want 403: %d %s", rec.Code, rec.Body)
	}
	// Already-registered devices are unaffected by the tightening (old devices pass).
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.1:1111", "hwid-aaa"); rec.Code != http.StatusOK {
		t.Fatalf("known device: %d %s", rec.Code, rec.Body)
	}
	// Requests without hwid skip the hwid-dimension limit (client never reported it,
	// so that dimension does not apply).
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.0.0.9:1111", ""); rec.Code != http.StatusOK {
		t.Fatalf("no-hwid request: %d %s", rec.Code, rec.Body)
	}
	// Alerts dedupe per user per day: multiple over-limit hits on the same day
	// write only one event.
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

	// Repeated hits from the same IP are not limited.
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.1:1000", ""); rec.Code != http.StatusOK {
		t.Fatalf("ip 1 first: %d %s", rec.Code, rec.Body)
	}
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.1:2000", ""); rec.Code != http.StatusOK {
		t.Fatalf("ip 1 again: %d %s", rec.Code, rec.Body)
	}
	// A new IP exceeds the limit: 403.
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

	// Rows outside the window don't occupy quota: manually insert an IP row from
	// 25h ago, and a new IP should be allowed (old row pruned in passing).
	var uid float64
	if err := db.Model(&storage.User{}).Select("id").Where("username = 'ipuser'").Scan(&uid).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := db.Create(&storage.SubDevice{UserID: uint(uid), Kind: "ip", Key: "10.1.9.9",
		FirstSeen: old, LastSeen: old}).Error; err != nil {
		t.Fatal(err)
	}
	// First clear the registered 10.1.0.1 to free the slot — delete the row
	// directly (the window test only checks whether old rows pass).
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

// TestSubDevicePruneUnlimited covers window pruning for users with no IP limit (P1-7):
// with IPLimit=0 old IP rows were never pruned and sub_devices grew without bound;
// after the fix, a new IP visit prunes the window in passing, keeping the table bounded.
func TestSubDevicePruneUnlimited(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, id := createLimitedUser(t, r, "unlim", 0, 0) // no device/IP limit at all
	old := time.Now().Add(-25 * time.Hour)
	if err := db.Create(&storage.SubDevice{UserID: uint(id), Kind: "ip", Key: "10.9.9.1",
		FirstSeen: old, LastSeen: old}).Error; err != nil {
		t.Fatal(err)
	}
	// New IP visit → old rows should be pruned, leaving only the new row.
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.0.5:1000", ""); rec.Code != http.StatusOK {
		t.Fatalf("new ip: %d %s", rec.Code, rec.Body)
	}
	var cnt int64
	if err := db.Model(&storage.SubDevice{}).Where("user_id = ? AND kind = 'ip'", uint(id)).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("ip rows = %d, want 1 (old pruned, new kept)", cnt)
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
	// The new token works.
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
	// Device list endpoint.
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
	if len(rows) != 2 { // one row each for ip + hwid
		t.Fatalf("devices rows = %d, want 2", len(rows))
	}
}

// TestPanelMeDeviceUsage covers panelMe device-dimension usage (P2-4 visibility loop):
// when subscription requests get the over-limit 403, the user can see registered counts
// and limits in the panel — registered hwid count and 24h-window IP count.
func TestPanelMeDeviceUsage(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	token := panelToken(t, r, "me-dev")
	var u storage.User
	if err := db.Where("username = ?", "me-dev").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.User{}).Where("id = ?", u.ID).
		Updates(map[string]any{"device_limit": 2, "ip_limit": 3}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stale := now.Add(-25 * time.Hour)
	rows := []storage.SubDevice{
		{UserID: u.ID, Kind: "hwid", Key: "dev-1", FirstSeen: now, LastSeen: now},
		{UserID: u.ID, Kind: "ip", Key: "10.2.0.1", FirstSeen: now, LastSeen: now},
		{UserID: u.ID, Kind: "ip", Key: "10.2.0.2", FirstSeen: now, LastSeen: now},
		{UserID: u.ID, Kind: "ip", Key: "10.2.0.9", FirstSeen: stale, LastSeen: stale},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	rec := doPanel(t, r, token, "GET", "/api/panel/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("panel me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		DeviceLimit int   `json:"device_limit"`
		IPLimit     int   `json:"ip_limit"`
		Devices     int64 `json:"devices"`
		Ips         int64 `json:"ips"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.DeviceLimit != 2 || me.IPLimit != 3 || me.Devices != 1 || me.Ips != 2 {
		t.Fatalf("panel me device usage = %+v, want 2/3/1/2", me)
	}
}

// TestUserDeviceUnbind pins the admin device unbind (F-1224): DELETE
// /api/users/:id/devices/:did removes exactly one binding scoped to that user
// (a device id belonging to another account never resolves), the list shrinks
// accordingly, and an unknown or already-unbound id gives 404.
func TestUserDeviceUnbind(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "unbind-a"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user a: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "unbind-b"}); rec.Code != http.StatusCreated {
		t.Fatalf("create user b: %d %s", rec.Code, rec.Body)
	}
	var ua, ub storage.User
	if err := db.Where("username = ?", "unbind-a").First(&ua).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("username = ?", "unbind-b").First(&ub).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rows := []storage.SubDevice{
		{UserID: ua.ID, Kind: "hwid", Key: "dev-a1", FirstSeen: now, LastSeen: now},
		{UserID: ua.ID, Kind: "hwid", Key: "dev-a2", FirstSeen: now, LastSeen: now},
		{UserID: ub.ID, Kind: "hwid", Key: "dev-b1", FirstSeen: now, LastSeen: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	// Cross-user: b's device id never resolves under a's route.
	if rec := doJSON(t, r, "DELETE", "/api/users/"+utoa(ua.ID)+"/devices/"+utoa(rows[2].ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user unbind = %d", rec.Code)
	}
	// Unbind a's first device.
	if rec := doJSON(t, r, "DELETE", "/api/users/"+utoa(ua.ID)+"/devices/"+utoa(rows[0].ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("unbind: %d %s", rec.Code, rec.Body)
	}
	var left []storage.SubDevice
	if err := db.Where("user_id = ?", ua.ID).Order("id").Find(&left).Error; err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Key != "dev-a2" {
		t.Fatalf("left = %+v, want only dev-a2", left)
	}
	// Already unbound: 404. Unknown id: 404.
	if rec := doJSON(t, r, "DELETE", "/api/users/"+utoa(ua.ID)+"/devices/"+utoa(rows[0].ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("re-unbind = %d", rec.Code)
	}
	if rec := doJSON(t, r, "DELETE", "/api/users/"+utoa(ua.ID)+"/devices/9999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown device = %d", rec.Code)
	}
	// b's binding is untouched.
	var cnt int64
	if err := db.Model(&storage.SubDevice{}).Where("user_id = ?", ub.ID).Count(&cnt).Error; err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("b devices = %d, want 1", cnt)
	}
}

// TestSubDeviceChurnAlert pins the abnormal-device alert (F-1226): a third
// distinct device inside the churn window lands exactly one device_anomaly
// event for the admin (deduped per user per day); a user under the threshold
// stays silent, and the alert never blocks delivery (all fetches stay 200).
func TestSubDeviceChurnAlert(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, _ := createLimitedUser(t, r, "churn-u", 0, 0) // unlimited: only the anomaly signal is under test

	for i, hw := range []string{"hw-churn-1", "hw-churn-2", "hw-churn-3"} {
		addr := fmt.Sprintf("10.7.0.%d:1111", i+1)
		if rec := getSubAt(t, r, path, "v2rayNG/1.8", addr, hw); rec.Code != http.StatusOK {
			t.Fatalf("device %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	var ev storage.Event
	if err := db.Where("kind = ?", "device_anomaly").First(&ev).Error; err != nil {
		t.Fatalf("no device_anomaly event: %v", err)
	}
	if ev.Severity != "warning" || ev.Target != "admin" {
		t.Fatalf("event shape = %+v", ev)
	}
	if !strings.Contains(ev.Body, "churn-u") {
		t.Fatalf("event body missing user: %s", ev.Body)
	}

	// Same-day dedup: a fourth device does not duplicate the event.
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.7.0.9:1111", "hw-churn-4"); rec.Code != http.StatusOK {
		t.Fatalf("device 4: %d %s", rec.Code, rec.Body)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = 'device_anomaly'").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("device_anomaly events = %d, want 1", n)
	}

	// A second user under the threshold (two devices) writes nothing.
	path2, _ := createLimitedUser(t, r, "calm-u", 0, 0)
	for i, hw := range []string{"hw-calm-1", "hw-calm-2"} {
		addr := fmt.Sprintf("10.8.0.%d:1111", i+1)
		if rec := getSubAt(t, r, path2, "v2rayNG/1.8", addr, hw); rec.Code != http.StatusOK {
			t.Fatalf("calm device %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	if err := db.Model(&storage.Event{}).Where("kind = 'device_anomaly'").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("device_anomaly events after calm user = %d, want 1", n)
	}
}
