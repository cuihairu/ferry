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

// poolEntryNode creates an in-pool entry node, returning the router, the database, and the node row.
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

// TestPoolListErrors covers pool-list read failures: a failed entry query
// returns 500, and a failed latest-verdict query for one node (not a
// record-not-found) returns 500.
func TestPoolListErrors(t *testing.T) {
	// Entry query failure -> 500.
	r, db, _ := poolEntryNode(t, "pl-a")
	regQueryFail("t:poolnodes", db, (*[]storage.Node)(nil))
	if rec := doJSON(t, r, "GET", "/api/pool", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes query fail: %d %s", rec.Code, rec.Body)
	}

	// Latest-verdict query failure -> 500 (node rows must exist to reach the per-node re-query).
	r2, db2, _ := poolEntryNode(t, "pl-b")
	regQueryFail("t:poolprobe", db2, (*storage.ProbeReport)(nil))
	if rec := doJSON(t, r2, "GET", "/api/pool", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("probe query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestPoolActionGuards covers a bad id 404 for suspend/resume/standby, a
// missing node 404 for standby, and a resume lookup failure (not
// record-not-found) 500.
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

	// Resume lookup failure -> the default 500 (the injection only blocks queries, and First gets a non-not-found error).
	regQueryFail("t:poolresume", db, (*storage.Node)(nil))
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(n.ID), 10)+"/resume", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("resume query fail: %d %s", rec.Code, rec.Body)
	}
}

// TestPoolOnChangeHook covers the per-region reconciliation hook (E-27)
// firing once each on a successful suspend/resume/standby; the test pool hook
// is a package-level variable, so restore it afterwards.
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
		// The hook runs in a goroutine, so poll until the counter catches up.
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
