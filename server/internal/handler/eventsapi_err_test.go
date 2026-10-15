package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 三十五批补口：事件 outbox（HERALD-1）错误口径——列表合法 limit 生效/
// 通道过滤/取数失败/状态计数失败、重投坏 id 与落库失败、回执读体失败/
// 两种形状的坏 JSON/落库失败、看板坏 limit 两种与合法 limit/取数失败/
// 聚合失败/降级健康标记、自检落库失败、样例取类失败与单类取数失败跳过。
// 留白：无。

// isEventModel 判断语句是否落在事件 outbox 表上。
func isEventModel(v any) bool {
	switch v.(type) {
	case *storage.Event, []storage.Event, *[]storage.Event:
		return true
	}
	return false
}

// isEventDeliveryModel 判断语句是否落在通道回执表上。
func isEventDeliveryModel(v any) bool {
	switch v.(type) {
	case *storage.EventDelivery, []storage.EventDelivery, *[]storage.EventDelivery:
		return true
	}
	return false
}

// seedEventRow 直落一行事件，返回其 ID。
func seedEventRow(t *testing.T, db *gorm.DB, kind, status string) int64 {
	t.Helper()
	row := storage.Event{Kind: kind, Severity: "info", Title: "t", Body: "b", Status: status}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return row.ID
}

// doRawBody 发原始字节体请求（不做 JSON 编码、不带凭据）：回执口挂开放
// 组，且坏 JSON 不能被编码器洗成合法形态。
func doRawBody(t *testing.T, r *gin.Engine, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// errReader 读即报错：专测读体失败分支。
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read fail") }

// TestListEventsErrors 列表：合法 limit 生效、通道过滤、取数失败与状态计
// 数失败。
func TestListEventsErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedEventRow(t, db, "l-k", herald.StatusPending)
	if rec := doJSON(t, r, http.MethodGet, "/api/events?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/events?kind=l-k&limit=1", nil); rec.Code != http.StatusOK {
		t.Fatalf("kind filter: %d %s", rec.Code, rec.Body)
	}

	// 列表取数失败（Find 走 gorm:query）。
	r, db = newTestRouterWithDB(t)
	failQueryOn(t, db, "test:event_find", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// 状态计数失败（Count 是聚合 Scan，走 gorm:row）。
	r, db = newTestRouterWithDB(t)
	seedEventRow(t, db, "c-k", herald.StatusSent)
	failRowOn(t, db, "test:event_count", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("count: %d %s", rec.Code, rec.Body)
	}
}

// TestRetryEventErrors 重投：坏 id 与落库失败。
func TestRetryEventErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/abc/retry", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}

	r, db := newTestRouterWithDB(t)
	id := seedEventRow(t, db, "r-k", herald.StatusFailed)
	failUpdateOn(t, db, "test:event_retry", isEventModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/"+strconv.FormatUint(uint64(id), 10)+"/retry", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("updates: %d %s", rec.Code, rec.Body)
	}
}

// TestEventResultErrors 回执：读体失败与两种形状的坏 JSON、落库失败。
func TestEventResultErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results", errReader{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("read body: %d %s", rec.Code, rec.Body)
	}
	// 整体坏 JSON：形状探测段即失败。
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results",
		strings.NewReader("{not json")); rec.Code != http.StatusBadRequest {
		t.Fatalf("probe json: %d %s", rec.Code, rec.Body)
	}
	// 旧形状（数字 event_id）字段类型不符：探测段过、二次解包失败。
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results",
		strings.NewReader(`{"event_id":1.5}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy json: %d %s", rec.Code, rec.Body)
	}
	// 回调形状（字符串 event_id）字段类型不符：回调解包失败。
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results",
		strings.NewReader(`{"event_id":"x","kind":5}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("callback json: %d %s", rec.Code, rec.Body)
	}

	// 回执落库失败（事件存在、回执表注错）。
	r, db := newTestRouterWithDB(t)
	id := seedEventRow(t, db, "d-k", herald.StatusPending)
	failCreateOn(t, db, "test:delivery_create", isEventDeliveryModel)
	if rec := doRawBody(t, r, http.MethodPost, "/api/internal/event-results",
		strings.NewReader(`{"event_id":`+strconv.FormatUint(uint64(id), 10)+`,"channel":"chan","status":"sent"}`)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create delivery: %d %s", rec.Code, rec.Body)
	}
}

// TestListEventDeliveriesErrors 看板：坏 limit 两种、合法 limit、取数失
// 败、聚合失败与降级健康标记。
func TestListEventDeliveriesErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=201"} {
		if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries"+q, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("limit %s: %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries?limit=5", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid limit: %d %s", rec.Code, rec.Body)
	}

	// 回执行取数失败（Find 走 gorm:query）。
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:delivery_find", isEventDeliveryModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("find: %d %s", rec.Code, rec.Body)
	}

	// 通道聚合失败（GROUP BY Scan 走 gorm:row）。
	r, db = newTestRouterWithDB(t)
	failRowOn(t, db, "test:delivery_agg", isEventDeliveryModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("agg: %d %s", rec.Code, rec.Body)
	}

	// 降级健康：同通道既有 sent 又有 failed → degraded 且回最近失败明细。
	r, db = newTestRouterWithDB(t)
	for _, st := range []string{herald.StatusSent, herald.StatusFailed} {
		if err := db.Create(&storage.EventDelivery{EventID: 1, Channel: "mail", Status: st,
			Detail: "boom", At: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, http.MethodGet, "/api/events/deliveries", nil)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), `"degraded"`) ||
		!containsStr(rec.Body.String(), "boom") {
		t.Fatalf("degraded health: %d %s", rec.Code, rec.Body)
	}
}

// TestTestEventError 通道自检落库失败 500。
func TestTestEventError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failCreateOn(t, db, "test:event_emit", isEventModel)
	if rec := doJSON(t, r, http.MethodPost, "/api/events/test", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("emit: %d %s", rec.Code, rec.Body)
	}
}

// TestListEventSamplesErrors 样例：取类失败与单类取数失败跳过（不下发半
// 个样例列表）。
func TestListEventSamplesErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	failQueryOn(t, db, "test:event_pluck", isEventModel)
	if rec := doJSON(t, r, http.MethodGet, "/api/events/samples", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("pluck: %d %s", rec.Code, rec.Body)
	}

	// 取类（第 1 查）放行、取样例（第 2 查）注错 → continue 跳过。
	r, db = newTestRouterWithDB(t)
	seedEventRow(t, db, "s-k", herald.StatusSent)
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:event_sample_first", func(tx *gorm.DB) {
		if isEventModel(tx.Statement.Model) || isEventModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	rec := doJSON(t, r, http.MethodGet, "/api/events/samples", nil)
	if rec.Code != http.StatusOK || containsStr(rec.Body.String(), "s-k") {
		t.Fatalf("skip failed sample: %d %s", rec.Code, rec.Body)
	}
}
