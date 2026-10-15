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

// failNthCount 让第 n 次 Count 查询失败（Count 目标都是 *int64，用序号区分
// 订阅链设备/IP 面的多次计数）。
func failNthCount(name string, db *gorm.DB, n int) {
	calls := 0
	db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*int64); ok {
			calls++
			if calls == n {
				tx.AddError(errors.New("db down"))
			}
		}
	})
}

// failTouchCreate 让指定 kind 的设备/IP 落库失败（非唯一索引冲突，走
// 非 dup 错误分支）。
func failTouchCreate(name string, db *gorm.DB, kind string) {
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if d, ok := tx.Statement.Dest.(*storage.SubDevice); ok && d.Kind == kind {
			tx.AddError(errors.New("db down"))
		}
	})
}

// TestSubDeviceCheckDBErrors 覆盖订阅链设备/IP 面各计数与落库失败分支：
// 订阅端统一按 403 拒访（客户端看不到库错细节）。
func TestSubDeviceCheckDBErrors(t *testing.T) {
	cases := []struct {
		name        string
		deviceLimit int
		ipLimit     int
		hwid        string
		failCount   int
		failKind    string
	}{
		{"ip exists count", 0, 0, "", 1, ""},
		{"ip limit count", 0, 1, "", 2, ""},
		{"ip touch create", 0, 0, "", 0, "ip"},
		{"hwid exists count", 0, 0, "hw-1", 2, ""},
		{"device limit count", 1, 0, "hw-1", 3, ""},
		{"hwid touch create", 0, 0, "hw-1", 0, "hwid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, db := newTestRouterWithDB(t)
			path, _ := createLimitedUser(t, r, "sd-lmt", tc.deviceLimit, tc.ipLimit)
			if tc.failCount > 0 {
				failNthCount("t:sdcnt", db, tc.failCount)
			}
			if tc.failKind != "" {
				failTouchCreate("t:sdtouch", db, tc.failKind)
			}
			if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.9.9.9:1111", tc.hwid); rec.Code != http.StatusForbidden {
				t.Fatalf("want 403: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

// TestSubDeviceLimitAlertError 覆盖超限告警去重计数失败时静默跳过（仍 403），
// 以及超限拒访按限值文案回报。
func TestSubDeviceLimitAlertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	path, _ := createLimitedUser(t, r, "sd-alert", 0, 1)
	// 首个 IP 落一行，第二个新 IP 触发上限判定。
	if rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.1.1:1111", ""); rec.Code != http.StatusOK {
		t.Fatalf("first ip: %d %s", rec.Code, rec.Body)
	}
	failNthCount("t:alertdedup", db, 3)
	rec := getSubAt(t, r, path, "v2rayNG/1.8", "10.1.1.2:1111", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("limit reached: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "ip limit reached") {
		t.Fatalf("limit message: %s", rec.Body)
	}
}

// TestUserDevicesErrors 覆盖设备列表的用户定位失败、无记录回空表、
// 明细取数失败分支。
func TestUserDevicesErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/users/999/devices", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d", rec.Code)
	}

	// 无记录 → 200 空表（不返回 null）。
	r2, _ := newTestRouterWithDB(t)
	_, uid := createLimitedUser(t, r2, "sd-list", 0, 0)
	rec := doJSON(t, r2, "GET", "/api/users/"+strconv.Itoa(int(uid))+"/devices", nil)
	if rec.Code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "[") {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body)
	}

	// 明细取数失败 → 500。
	r3, db3 := newTestRouterWithDB(t)
	_, uid3 := createLimitedUser(t, r3, "sd-err", 0, 0)
	regQueryFail("t:devrows", db3, (*[]storage.SubDevice)(nil))
	if rec := doJSON(t, r3, "GET", "/api/users/"+strconv.Itoa(int(uid3))+"/devices", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows fail: %d %s", rec.Code, rec.Body)
	}
}
