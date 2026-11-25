package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserStateSplitFields covers the three G3 paths through the admin API:
// expired-not-limited, limited-not-expired, and both. The two judgments have to
// be reported independently, with status naming the primary cause.
func TestUserStateSplitFields(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	expiredAt := now.Add(-time.Hour)
	quota := int64(100)

	// create each user and keep its id: the seeded admin row already occupies a
	// row in this test database, so ids are not the literal 1/2/3.
	ids := map[string]uint{}
	for _, c := range []struct{ username, key string }{
		{"g3-expired", "expired"}, {"g3-quota", "quota"}, {"g3-both", "both"},
	} {
		body := map[string]any{"username": c.username, "quota_bytes": quota}
		if c.key == "expired" || c.key == "both" {
			body["expires_at"] = expiredAt.Format(time.RFC3339Nano)
		}
		rec := doJSON(t, r, "POST", "/api/users", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", c.key, rec.Code, rec.Body)
		}
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode %s: %v", c.key, err)
		}
		ids[c.key] = uint(v["id"].(float64))
	}

	// usage: user 1 stays under quota, users 2 and 3 exhaust it
	for _, row := range []storage.TrafficLog{
		{UserID: ptrUint(ids["expired"]), RxBytes: 10, RecordedAt: now},
		{UserID: ptrUint(ids["quota"]), RxBytes: quota, RecordedAt: now},
		{UserID: ptrUint(ids["both"]), RxBytes: quota, RecordedAt: now},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("insert traffic: %v", err)
		}
	}

	want := map[uint]struct {
		status  string
		expired bool
		limited bool
	}{
		ids["expired"]: {"expired", true, false},
		ids["quota"]:   {"limited", false, true},
		ids["both"]:    {"expired", true, true},
	}
	for id, w := range want {
		t.Run("user "+strconv.FormatUint(uint64(id), 10), func(t *testing.T) {
			rec := doJSON(t, r, "GET", "/api/users/"+strconv.FormatUint(uint64(id), 10), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("get user: %d %s", rec.Code, rec.Body)
			}
			var v map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if v["status"] != w.status || v["expired"] != w.expired || v["limited"] != w.limited {
				t.Fatalf("user %d = status:%v expired:%v limited:%v, want status:%s expired:%v limited:%v",
					id, v["status"], v["expired"], v["limited"], w.status, w.expired, w.limited)
			}
		})
	}

	// a disabled account reports disabled regardless of expiry and quota
	disabledID := strconv.FormatUint(uint64(ids["both"]), 10)
	if rec := doJSON(t, r, "PUT", "/api/users/"+disabledID, map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("disable user: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "GET", "/api/users/"+disabledID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get disabled user: %d", rec.Code)
	}
	var disabled map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &disabled)
	if disabled["status"] != "disabled" {
		t.Fatalf("disabled user = %+v", disabled)
	}
}

func ptrUint(v uint) *uint { return &v }
