package handler

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDeleteUserKeepsTrafficHistory covers the P2 deleteUser fix for
// cascade-deleting billing history: after deleting a user, traffic_logs
// ledger rows survive with user_id nulled (detach ownership, don't delete
// the ledger), subscription device/IP safety state clears with the user, and
// the user row itself 404s. Before the fix the User foreign key was CASCADE
// and deleting a user wiped out all billing history.
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
	// Two billing rows + one device row (safety state, should clear with the user).
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
	if rec := doJSON(t, r, "DELETE", "/api/users/"+idStr, map[string]any{"confirm": "hist"}); rec.Code != http.StatusOK {
		t.Fatalf("delete user: %d %s", rec.Code, rec.Body)
	}

	// User row deleted (404), billing history kept (2 rows), ownership nulled.
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
