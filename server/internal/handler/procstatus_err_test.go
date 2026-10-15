package handler

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestSaveProcStatusesEdges 覆盖落库函数三条边界：空进程名整条跳过不落行；
// 既有进程查询的非 NotFound 失败原样返回；既有行更新失败原样返回。
func TestSaveProcStatusesEdges(t *testing.T) {
	// 空进程名跳过：不返回错误也不落行。
	r, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if err := h.saveProcStatuses(1, []agentproto.ProcStatus{{Name: "", State: "running"}}); err != nil {
		t.Fatalf("empty proc name: %v", err)
	}
	var n int64
	if err := db.Model(&storage.NodeProcStatus{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("proc rows = %d, want 0", n)
	}

	// 既有进程查询的非 NotFound 失败 → 返回错误（与未落行区分）。
	r, db = newTestRouterWithDB(t)
	h = &Handler{db: db}
	regQueryFail("t:ps_first", db, (*storage.NodeProcStatus)(nil))
	if err := h.saveProcStatuses(1, []agentproto.ProcStatus{{Name: "cache", State: "running"}}); err == nil {
		t.Fatal("first error should propagate")
	}
	_ = r

	// 既有行更新失败 → 返回错误。
	r, db = newTestRouterWithDB(t)
	h = &Handler{db: db}
	if err := db.Create(&storage.NodeProcStatus{NodeID: 1, Proc: "cache", State: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	db.Callback().Update().Before("gorm:update").Register("t:ps_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.NodeProcStatus); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if err := h.saveProcStatuses(1, []agentproto.ProcStatus{{Name: "cache", State: "stopped"}}); err == nil {
		t.Fatal("update error should propagate")
	}
	_ = r
}

// TestNodeProcsDBErrors 覆盖 GET /api/nodes/:id/procs 取数失败 500。
func TestNodeProcsDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, _ := createNodeFor(t, r, "ps-api")
	regQueryFail("t:ps_list", db, (*[]storage.NodeProcStatus)(nil))
	if rec := doJSON(t, r, http.MethodGet, fmt.Sprintf("/api/nodes/%d/procs", id), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list procs: %d %s", rec.Code, rec.Body)
	}
}
