package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// landingFixture 落一个落地节点与一条区域级分配，返回分配 id。
func landingFixture(t *testing.T, r *gin.Engine) uint {
	t.Helper()
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "land-db", "address": "land-db.example.com", "port": 443,
		"protocol": "vless", "config": `{"uuid":"u1"}`, "role": "landing",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("node: %d %s", rec.Code, rec.Body)
	}
	var n map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	lid := uint(n["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": lid, "direction": "in", "weight": 30,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("landing: %d %s", rec.Code, rec.Body)
	}
	var row map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &row)
	return uint(row["id"].(float64))
}

// TestLandingDBErrors 覆盖落地分配的取数与落库失败：列表取数、建档落库、
// 权重更新、释放回写、定位非查无错误五路 500。
func TestLandingDBErrors(t *testing.T) {
	// 列表取数失败 → 500。
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:landlist", db, (*[]storage.LandingAssignment)(nil))
	if rec := doJSON(t, r, "GET", "/api/landings", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	// 建档落库失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	if rec := doJSON(t, r2, "POST", "/api/nodes", map[string]any{
		"name": "land-c", "address": "land-c.example.com", "port": 443,
		"protocol": "vless", "config": `{"uuid":"u1"}`, "role": "landing",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("node: %d %s", rec.Code, rec.Body)
	}
	db2.Callback().Create().Before("gorm:create").Register("t:landcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.LandingAssignment); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r2, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": 1, "direction": "in", "weight": 30,
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// 权重更新失败 → 500。
	r3, db3 := newTestRouterWithDB(t)
	id3 := landingFixture(t, r3)
	db3.Callback().Update().Before("gorm:update").Register("t:landupd", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.LandingAssignment); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r3, "PUT", fmt.Sprintf("/api/landings/%d", id3), map[string]any{"weight": 50}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}

	// 释放回写失败 → 500。
	r4, db4 := newTestRouterWithDB(t)
	id4 := landingFixture(t, r4)
	db4.Callback().Update().Before("gorm:update").Register("t:landrel", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.LandingAssignment); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r4, "DELETE", fmt.Sprintf("/api/landings/%d", id4), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}

	// 定位非查无错误 → 500（区分 404 的 gorm 记录不存在）。
	r5, db5 := newTestRouterWithDB(t)
	id5 := landingFixture(t, r5)
	regQueryFail("t:landfind", db5, (*storage.LandingAssignment)(nil))
	if rec := doJSON(t, r5, "PUT", fmt.Sprintf("/api/landings/%d", id5), map[string]any{"weight": 50}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}
}
