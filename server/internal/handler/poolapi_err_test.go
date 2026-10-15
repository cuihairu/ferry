package handler

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// poolEntryNode 建一个在池入口节点，返回路由、库与节点行。
func poolEntryNode(t *testing.T, name string) (*gin.Engine, *gorm.DB, storage.Node) {
	t.Helper()
	r, db := newTestRouterWithDB(t)
	n := storage.Node{Name: name, Address: name + ".example.com", Port: 443,
		Protocol: "vless", Token: "t-" + name, Role: "entry", Enabled: true,
		Config: `{"uuid":"u"}`}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return r, db, n
}

// TestPoolListErrors 覆盖池列表取数失败：入口查询失败 500、单节点窗口内
// 最新结论查询失败（非查无）500。
func TestPoolListErrors(t *testing.T) {
	// 入口查询失败 → 500。
	r, db, _ := poolEntryNode(t, "pl-a")
	regQueryFail("t:poolnodes", db, (*[]storage.Node)(nil))
	if rec := doJSON(t, r, "GET", "/api/pool", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes query fail: %d %s", rec.Code, rec.Body)
	}

	// 最新结论查询失败 → 500（有节点行才会进逐节点回查）。
	r2, db2, _ := poolEntryNode(t, "pl-b")
	regQueryFail("t:poolprobe", db2, (*storage.ProbeReport)(nil))
	if rec := doJSON(t, r2, "GET", "/api/pool", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("probe query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestPoolActionGuards 覆盖摘除/复位/预备的坏 id 404、预备查无 404、
// 复位定位失败（非查无）500。
func TestPoolActionGuards(t *testing.T) {
	r, db, n := poolEntryNode(t, "pg-a")
	for _, p := range []string{"suspend", "resume", "standby"} {
		if rec := doJSON(t, r, "POST", "/api/pool/abc/"+p, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("%s bad id: %d %s", p, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, "POST", "/api/pool/424242/standby", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("standby missing: %d", rec.Code)
	}

	// 复位定位失败 → default 500（注错只拦查询，First 非查无错误）。
	regQueryFail("t:poolresume", db, (*storage.Node)(nil))
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(n.ID), 10)+"/resume", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("resume query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestPoolOnChangeHook 覆盖摘除/复位/预备成功时分地域对账钩子（E-27）
// 被触发各一次；测试池钩子是包级变量，用后恢复。
func TestPoolOnChangeHook(t *testing.T) {
	old := pool.OnChange
	var mu sync.Mutex
	hits := 0
	pool.OnChange = func(*gorm.DB) {
		mu.Lock()
		hits++
		mu.Unlock()
	}
	t.Cleanup(func() { pool.OnChange = old })

	r, _, n := poolEntryNode(t, "pc-a")
	path := "/api/pool/" + strconv.FormatUint(uint64(n.ID), 10)

	for _, p := range []string{"suspend", "resume", "standby"} {
		before := hits
		if rec := doJSON(t, r, "POST", path+"/"+p, nil); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
		}
		// 钩子在 goroutine 里跑，轮询等计数到位。
		deadline := time.Now().Add(5 * time.Second)
		for {
			mu.Lock()
			got := hits
			mu.Unlock()
			if got > before {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: onchange hook not called", p)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
