package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestUserCRUD(t *testing.T) {
	r := newTestRouter(t)

	// 创建：生成订阅令牌，默认启用、配额 0（不限）
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "alice", "quota_bytes": 1073741824,
		"expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["username"] != "alice" || u["enabled"] != true {
		t.Fatalf("created user mismatch: %v", u)
	}
	token := u["sub_token"].(string)
	if len(token) < 40 {
		t.Fatalf("sub_token too short: %q", token)
	}
	_ = u["id"].(float64) // 详情用例走字面 id

	// 重名拒绝
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "alice"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate username: %d %s", rec.Code, rec.Body)
	}

	// 列表与详情
	if rec := doJSON(t, r, "GET", "/api/users", nil); rec.Code != http.StatusOK {
		t.Fatalf("list users: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/users/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get user: %d", rec.Code)
	}

	// 更新：配额、到期清除、停用
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{
		"quota_bytes": 0, "clear_expire": true, "enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update user: %d %s", rec.Code, rec.Body)
	}
	var fresh map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &fresh)
	if fresh["quota_bytes"].(float64) != 0 || fresh["enabled"] != false {
		t.Fatalf("updated user mismatch: %v", fresh)
	}
	if fresh["expires_at"] != nil {
		t.Fatalf("expires_at should be cleared: %v", fresh["expires_at"])
	}

	// 改名冲突与改名成功
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bob"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bob: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"username": "bob"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("rename to taken username: %d", rec.Code)
	}
	rec = doJSON(t, r, "PUT", "/api/users/1", map[string]any{"username": "alice2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}

	// 重置订阅令牌：旧令牌失效，新令牌不同
	rec = doJSON(t, r, "POST", "/api/users/1/sub-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset sub token: %d %s", rec.Code, rec.Body)
	}
	var afterReset map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &afterReset)
	if afterReset["sub_token"].(string) == token {
		t.Fatal("sub_token must change on reset")
	}

	// 删除：连带流量记录
	if rec := doJSON(t, r, "DELETE", "/api/users/1", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete user: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/users/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted user should 404: %d", rec.Code)
	}
}

func TestUserValidation(t *testing.T) {
	r := newTestRouter(t)

	// 空用户名
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank username: %d", rec.Code)
	}
	// 负配额
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "x", "quota_bytes": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative quota: %d", rec.Code)
	}
	// 非法 id
	if rec := doJSON(t, r, "GET", "/api/users/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d", rec.Code)
	}

	// 显式停用创建不被 default:true 吞掉
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "off", "enabled": false})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create disabled user: %d %s", rec.Code, rec.Body)
	}
	var off map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &off)
	if off["enabled"] != false {
		t.Fatalf("created disabled user came back enabled: %v", off["enabled"])
	}
}
