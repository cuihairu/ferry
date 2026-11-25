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

// TestSaveProcStatusesEdges covers three edges of the persistence function: an empty
// proc name is skipped as a whole and writes no row; a non-NotFound failure from the
// existing-process lookup is returned as-is; an update failure on an existing row is
// returned as-is.
func TestSaveProcStatusesEdges(t *testing.T) {
	// Empty proc name is skipped: no error returned and no row written.
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

	// A non-NotFound failure from the existing-process lookup -> error returned (distinct from "no row written").
	r, db = newTestRouterWithDB(t)
	h = &Handler{db: db}
	regQueryFail("t:ps_first", db, (*storage.NodeProcStatus)(nil))
	if err := h.saveProcStatuses(1, []agentproto.ProcStatus{{Name: "cache", State: "running"}}); err == nil {
		t.Fatal("first error should propagate")
	}
	_ = r

	// Update failure on an existing row -> error returned.
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

// TestNodeProcsDBErrors covers GET /api/nodes/:id/procs data fetch failure -> 500.
func TestNodeProcsDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, _ := createNodeFor(t, r, "ps-api")
	regQueryFail("t:ps_list", db, (*[]storage.NodeProcStatus)(nil))
	if rec := doJSON(t, r, http.MethodGet, fmt.Sprintf("/api/nodes/%d/procs", id), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list procs: %d %s", rec.Code, rec.Body)
	}
}
