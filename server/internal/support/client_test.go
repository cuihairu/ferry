package support

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// stubServify 记录调用并按脚本回状态，供宿主侧客户端用例跟跑。
type stubServify struct {
	t      *testing.T
	srv    *httptest.Server
	calls  []string
	cust   map[uint64]map[string]any // id -> 档案
	nextID uint64
	// onCreate 不为空时接管建档响应（模拟 409 冲突等分支）。
	onCreate func(w http.ResponseWriter, body map[string]any)
}

func newStubServify(t *testing.T) *stubServify {
	s := &stubServify{t: t, cust: map[uint64]map[string]any{}, nextID: 100}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/guest/session", func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, "guest-session")
		if r.Header.Get("X-API-Key") != "sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok-" + req.SessionID,
			"token_type":   "bearer",
			"expires_at":   int64(1900000000),
		})
	})
	mux.HandleFunc("GET /api/customers", func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, "search")
		email := r.URL.Query().Get("search")
		hits := []map[string]any{}
		for id, c := range s.cust {
			if c["email"] == email {
				hits = append(hits, map[string]any{"id": id, "email": email})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": hits})
	})
	mux.HandleFunc("POST /api/customers", func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, "create")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s.onCreate != nil {
			s.onCreate(w, body)
			return
		}
		s.nextID++
		body["id"] = s.nextID
		s.cust[s.nextID] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("PUT /api/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, "update")
		id, _ := strconv.ParseUint(r.PathValue("id"), 10, 64)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c, ok := s.cust[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		for k, v := range body {
			c[k] = v
		}
		_ = json.NewEncoder(w).Encode(c)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func TestGuestToken(t *testing.T) {
	s := newStubServify(t)
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
	tok, exp, err := cl.GuestToken(context.Background(), "ferry_user_42")
	if err != nil {
		t.Fatalf("GuestToken: %v", err)
	}
	if tok != "tok-ferry_user_42" || exp != 1900000000 {
		t.Fatalf("got %q %d", tok, exp)
	}
}

func TestSyncCustomerCreatesWhenMissing(t *testing.T) {
	s := newStubServify(t)
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
	err := cl.SyncCustomer(context.Background(), CustomerSnapshot{
		Email: "u7@users.ferry.local", Name: "carol", Notes: "状态：启用；流量：0/1073741824 字节（0%）",
	})
	if err != nil {
		t.Fatalf("SyncCustomer: %v", err)
	}
	if len(s.calls) != 2 || s.calls[0] != "search" || s.calls[1] != "create" {
		t.Fatalf("calls = %v", s.calls)
	}
	created := s.cust[s.nextID]
	if created["email"] != "u7@users.ferry.local" || created["source"] != "ferry" || created["name"] != "carol" {
		t.Fatalf("created = %v", created)
	}
}

func TestSyncCustomerUpdatesWhenFound(t *testing.T) {
	s := newStubServify(t)
	s.cust[7] = map[string]any{"id": 7, "email": "u7@users.ferry.local", "notes": "旧快照"}
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
	err := cl.SyncCustomer(context.Background(), CustomerSnapshot{
		Email: "u7@users.ferry.local", Name: "carol", Notes: "新快照",
	})
	if err != nil {
		t.Fatalf("SyncCustomer: %v", err)
	}
	if len(s.calls) != 2 || s.calls[1] != "update" {
		t.Fatalf("calls = %v", s.calls)
	}
	if s.cust[7]["notes"] != "新快照" {
		t.Fatalf("notes = %v", s.cust[7]["notes"])
	}
}

func TestSyncCustomerConflictFallsBackToUpdate(t *testing.T) {
	s := newStubServify(t)
	s.onCreate = func(w http.ResponseWriter, body map[string]any) {
		// 模拟并发建档撞唯一约束：先 409，档案其实已存在。
		s.cust[7] = map[string]any{"id": 7, "email": body["email"], "notes": "并发建档的旧快照"}
		w.WriteHeader(http.StatusConflict)
	}
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
	err := cl.SyncCustomer(context.Background(), CustomerSnapshot{
		Email: "u7@users.ferry.local", Name: "carol", Notes: "回填快照",
	})
	if err != nil {
		t.Fatalf("SyncCustomer: %v", err)
	}
	if s.cust[7]["notes"] != "回填快照" {
		t.Fatalf("notes = %v", s.cust[7]["notes"])
	}
}

func TestServerErrorsSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cl := &Client{BaseURL: srv.URL, ServiceKey: "sk-test"}
	if err := cl.SyncCustomer(context.Background(), CustomerSnapshot{Email: "x@y"}); err == nil {
		t.Fatal("want error on 500")
	}

	s := newStubServify(t)
	bad := &Client{BaseURL: s.srv.URL, ServiceKey: "wrong"}
	if _, _, err := bad.GuestToken(context.Background(), "s"); err == nil {
		t.Fatal("want error on 401")
	}
}
