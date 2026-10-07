package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
)

func TestEffectiveDownMbps(t *testing.T) {
	cases := []struct {
		plan, measured int
		effective      int
		accepted       bool
	}{
		{0, 50, 50, true},      // 无套餐以实测为准
		{100, 0, 100, false},   // 无效实测保留套餐
		{100, 110, 100, false}, // 偏差 10% 保留套餐
		{100, 50, 50, true},    // 偏差 50% 采纳实测
		{100, 200, 200, true},  // 虚标低套餐采纳实测
		{100, 130, 100, false}, // 边界 30% 不采纳
		{100, 131, 131, true},  // 超过 30% 采纳
	}
	for _, c := range cases {
		eff, acc := effectiveDownMbps(c.plan, c.measured)
		if eff != c.effective || acc != c.accepted {
			t.Fatalf("plan=%d measured=%d: got (%d,%v), want (%d,%v)",
				c.plan, c.measured, eff, acc, c.effective, c.accepted)
		}
	}
}

func TestSpeedtestBytes(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bytes: %d %s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 65536 {
		t.Fatalf("len = %d", rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content-type = %q", ct)
	}

	// 两次相同 n 字节一致（确定性），且不可压缩（随机性 sanity）
	rec2 := doJSON(t, r, "GET", "/api/speedtest/bytes?n=65536", nil)
	if rec2.Body.String() != rec.Body.String() {
		t.Fatal("bytes not deterministic")
	}

	for _, bad := range []string{"n=10", "n=99999999", "n=abc"} {
		if rec := doJSON(t, r, "GET", "/api/speedtest/bytes?"+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
}

func TestAgentWSCalibrate(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "cal-1", "address": "cal.example.com", "port": 443, "protocol": "vless",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	var node map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &node)
	token := node["token"].(string)
	id := int(node["id"].(float64))
	// 套餐 100M（E-9 列暂无 API，测试直写）
	if err := db.Model(&storage.Node{}).Where("id=?", id).
		Updates(map[string]any{"bw_down_mbps": 100, "meta_init": true}).Error; err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: "cal-1", Version: "test",
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 实测 50M：偏差 50% → 采纳实测
	cal, _ := agentproto.NewEnvelope("cal-1", agentproto.MsgCalibrate, agentproto.Calibrate{
		MeasuredDownMbps: 50, Bytes: 4 << 20, Seconds: 0.64,
	})
	if err := c.WriteJSON(cal); err != nil {
		t.Fatalf("write calibrate: %v", err)
	}
	env := readEnv(t, c)
	if env.Type != agentproto.MsgCalibrateAck {
		t.Fatalf("expected calibrate_ack, got %s", env.Type)
	}
	var ack agentproto.CalibrateAck
	if err := env.Decode(&ack); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !ack.Accepted || ack.EffectiveDownMbps != 50 {
		t.Fatalf("ack = %+v", ack)
	}
	var n storage.Node
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	if n.SpeedMeasuredMbps != 50 || n.SpeedCalibratedAt == nil || time.Since(*n.SpeedCalibratedAt) > time.Minute {
		t.Fatalf("calibration not recorded: %+v", n)
	}

	// 实测 95M：偏差 5% → 保留套餐，但实测值照记
	cal2, _ := agentproto.NewEnvelope("cal-2", agentproto.MsgCalibrate, agentproto.Calibrate{
		MeasuredDownMbps: 95, Bytes: 4 << 20, Seconds: 0.34,
	})
	if err := c.WriteJSON(cal2); err != nil {
		t.Fatalf("write calibrate: %v", err)
	}
	env = readEnv(t, c)
	var ack2 agentproto.CalibrateAck
	if err := env.Decode(&ack2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ack2.Accepted || ack2.EffectiveDownMbps != 100 {
		t.Fatalf("ack2 = %+v", ack2)
	}
	var n2 storage.Node
	if err := db.First(&n2, id).Error; err != nil {
		t.Fatal(err)
	}
	if n2.SpeedMeasuredMbps != 95 {
		t.Fatalf("measured not updated: %+v", n2)
	}
	_ = fmt.Sprint()
}
