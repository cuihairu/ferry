package cost

import (
	"bytes"
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestGroupEmptyKey 区域/运营商为空：归入「未知」而不是落空键。
func TestGroupEmptyKey(t *testing.T) {
	by := map[string]*GroupCost{}
	group(by, "", NodeCost{TrafficCostCents: 1, FixedCostCents: 2, ProjectedCents: 3})
	g, ok := by["未知"]
	if !ok || g.Nodes != 1 || g.TrafficCents != 1 || g.FixedCents != 2 || g.ProjectedCents != 3 {
		t.Fatalf("empty key must map to 未知: %#v", by)
	}
}

// TestGetThresholdDirtyValue 阈值存了脏数据（非数字/负数）：回缺省不报错。
func TestGetThresholdDirtyValue(t *testing.T) {
	db := newTestDB(t)
	for _, dirty := range []string{"abc", "-5"} {
		if err := storage.SetSetting(db, settingThreshold, dirty); err != nil {
			t.Fatal(err)
		}
		cents, err := GetThreshold(db)
		if err != nil || cents != DefaultThresholdCents {
			t.Fatalf("dirty %q: cents=%d err=%v; want default", dirty, cents, err)
		}
	}
}

// TestBuildOnBrokenDB 底层连接已关：Build/CheckAlerts 原样上抛，Loop 记日志
// 继续下一轮。
func TestBuildOnBrokenDB(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(db, time.Now()); err == nil {
		t.Fatal("build on closed db must error")
	}
	if err := CheckAlerts(db, time.Now(), log.New(io.Discard, "", 0)); err == nil {
		t.Fatal("check alerts on closed db must error")
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	Loop(ctx, db, 0, log.New(&buf, "", 0)) // interval<=0 回落缺省；检查失败只记日志
	if !strings.Contains(buf.String(), "cost check") {
		t.Fatalf("loop error not logged: %q", buf.String())
	}
}

// TestCheckAlertsNotifiesWebhook 超阈节点告警激活时外发 webhook：发送失败
// 只记日志，不影响告警本身。
func TestCheckAlertsNotifiesWebhook(t *testing.T) {
	db := newTestDB(t)
	n := seedNode(t, db, "costly", "hk", "按流量", 1000, 0)    // 1000分/GB
	seedTraffic(t, db, n.ID, 60_000_000_000, 0, time.Now()) // 60GB → 60000分 > 缺省 5000
	if err := storage.SetSetting(db, notify.KeyURL, "http://127.0.0.1:1/hook"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := CheckAlerts(db, time.Now(), log.New(&buf, "", 0)); err != nil {
		t.Fatalf("check alerts: %v", err)
	}
	if !strings.Contains(buf.String(), "cost alert") {
		t.Fatalf("alert not logged: %q", buf.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "notify webhook") {
		if time.Now().After(deadline) {
			t.Fatalf("webhook failure not logged: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
