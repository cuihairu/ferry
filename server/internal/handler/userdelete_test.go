package handler

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDeleteUserKeepsTrafficHistory 覆盖 P2 deleteUser 级联删计费历史修复：
// 删除用户后 traffic_logs 记账明细保留且 user_id 置空（摘归属不删账），
// 订阅设备/IP 安全态随用户清除，用户行本身 404。修复前 User 外键
// CASCADE，删除用户连带清掉全部计费历史。
func TestDeleteUserKeepsTrafficHistory(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "hist", "quota_bytes": 1 << 30})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", "hist").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	// 落两条计费记录 + 一条设备记录（安全态，应随用户清除）。
	uid := u.ID
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid, RxBytes: 100, TxBytes: 50, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.TrafficLog{
		UserID: &uid, RxBytes: 30, TxBytes: 20, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.SubDevice{
		UserID: u.ID, Kind: "ip", Key: "198.51.100.1", FirstSeen: time.Now(), LastSeen: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	idStr := strconv.FormatUint(uint64(u.ID), 10)
	if rec := doJSON(t, r, "DELETE", "/api/users/"+idStr, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete user: %d %s", rec.Code, rec.Body)
	}

	// 用户行删除（404），计费历史保留（2 行），归属置空。
	if rec := doJSON(t, r, "GET", "/api/users/"+idStr, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user should 404: %d", rec.Code)
	}
	var rest int64
	if err := db.Model(&storage.TrafficLog{}).Count(&rest).Error; err != nil {
		t.Fatal(err)
	}
	if rest != 2 {
		t.Fatalf("traffic logs should survive user delete, remain %d; want 2", rest)
	}
	var attached int64
	if err := db.Model(&storage.TrafficLog{}).Where("user_id IS NOT NULL").Count(&attached).Error; err != nil {
		t.Fatal(err)
	}
	if attached != 0 {
		t.Fatalf("traffic logs should be detached (user_id null), got %d", attached)
	}
	var subDevices int64
	if err := db.Model(&storage.SubDevice{}).Where("user_id = ?", u.ID).Count(&subDevices).Error; err != nil {
		t.Fatal(err)
	}
	if subDevices != 0 {
		t.Fatalf("sub devices should be cleared with user, got %d", subDevices)
	}
}
