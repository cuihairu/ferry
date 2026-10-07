package recovery

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

type fakeAction struct {
	name  string
	err   error
	runs  atomic.Int32
	sleep time.Duration
}

func (f *fakeAction) Name() string { return f.name }
func (f *fakeAction) Run(ctx context.Context, node storage.Node) error {
	f.runs.Add(1)
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

// suspendedNode 建一个被自动摘除且持续未恢复的入口节点。
func suspendedNode(t *testing.T, db *gorm.DB, name, reason string, changedAt time.Time) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Port: 443, Protocol: "vless", Enabled: true, Token: "t-" + name,
		Status: "online", Role: "entry", PoolState: "suspended", PoolReason: reason, PoolChangedAt: &changedAt}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func loadRecovery(t *testing.T, db *gorm.DB, nodeID uint) storage.Recovery {
	t.Helper()
	var rec storage.Recovery
	if err := db.Where("node_id = ?", nodeID).First(&rec).Error; err != nil {
		t.Fatalf("recovery row: %v", err)
	}
	return rec
}

// waitRecovery 轮询等待恢复行满足条件（动作回写在 goroutine 中）。
func waitRecovery(t *testing.T, db *gorm.DB, nodeID uint, cond func(storage.Recovery) bool) storage.Recovery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := loadRecovery(t, db, nodeID)
		if cond(rec) {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery never satisfied: %+v", rec)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSweepOpenAndAdvance 覆盖判封开线与分级推进：摘除持续未恢复开线，
// L1/L2 未注册 skipped 推进，L3 注册动作启动；手动摘除不开线。
func TestSweepOpenAndAdvance(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	suspendedNode(t, db, "manual-1", "手动摘除", now.Add(-15*time.Minute))

	l3 := &fakeAction{name: "new_instance"}
	reg := Registry{3: l3}

	// 第一轮：判封开线（含手动摘除排除）。
	if err := Sweep(db, now, Options{}, reg, nil); err != nil {
		t.Fatal(err)
	}
	rec := loadRecovery(t, db, 1)
	if rec.State != StateRunning || rec.Level != 1 {
		t.Fatalf("opened = %+v", rec)
	}
	var manual int64
	db.Model(&storage.Recovery{}).Where("node_name = ?", "manual-1").Count(&manual)
	if manual != 0 {
		t.Fatal("manual suspend must not open recovery")
	}

	// 后续轮次：L1/L2 skipped 推进 → L3 启动。
	for i := 0; i < 3; i++ {
		if err := Sweep(db, now, Options{}, reg, nil); err != nil {
			t.Fatal(err)
		}
	}
	// L3 动作很快跑完（fake），等已启动（action 落名）即可，不卡 running 态。
	rec = waitRecovery(t, db, 1, func(r storage.Recovery) bool {
		return r.Level == 3 && r.Action == "new_instance"
	})
	if rec.ActionState != ActionRunning && rec.ActionState != ActionOK {
		t.Fatalf("action_state = %+v", rec)
	}
}

// TestRecoveryDoneOnResume 覆盖探测恢复即完成：摘挂复位（pool active）后
// 流水线收尾 done，不写失败原因。
func TestRecoveryDoneOnResume(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-2", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 2, State: StateRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if got.State != StateDone || got.FinishedAt == nil || got.LastErr != "" {
		t.Fatalf("done = %+v", got)
	}
}

// TestTimeoutExhaustFails 覆盖级超时推进与全级耗尽：L3 动作超时 →
// 无下一级 → 流水线 failed（升级人工由 BR-5 承接）。
func TestTimeoutExhaustFails(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-3", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	start := now.Add(-11 * time.Minute) // 超过默认 LevelTimeout 10 分钟
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 3, State: StateRunning,
		Action: "new_instance", ActionState: ActionRunning,
		LevelStartedAt: start, StartedAt: start, UpdatedAt: start}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, Registry{3: &fakeAction{name: "new_instance"}}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if got.State != StateFailed || got.LastErr == "" {
		t.Fatalf("failed = %+v", got)
	}
}

// TestActionFailedAdvances 覆盖动作失败立即进下一级并最终耗尽：L3 动作
// 报错 → 流水线 failed 留原因。
func TestActionFailedAdvances(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-4", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 3, State: StateRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	l3 := &fakeAction{name: "new_instance", err: context.DeadlineExceeded}
	if err := Sweep(db, now, Options{}, Registry{3: l3}, nil); err != nil {
		t.Fatal(err)
	}
	// 动作在 goroutine 中失败回写，等落库后再扫一轮推进到终态。
	waitRecovery(t, db, n.ID, func(r storage.Recovery) bool { return r.ActionState == ActionFailed })
	if err := Sweep(db, now, Options{}, Registry{3: l3}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if l3.runs.Load() != 1 {
		t.Fatalf("runs = %d", l3.runs.Load())
	}
	if got.State != StateFailed || got.LastErr == "" {
		t.Fatalf("failed = %+v", got)
	}
}

// TestInstanceActionL3 覆盖 L3 一键开新机走供给流水线：预签发替换节点行、
// tofu apply 成功（注入 fake runner）、job 留痕 ok；凭证缺失失败留原因。
func TestInstanceActionL3(t *testing.T) {
	db := newTestDB(t)
	store := secret.NewStore("recovery-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "replace", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "applied", nil
	})
	a := &InstanceAction{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(name string) provision.LaunchParams {
			return provision.LaunchParams{
				InstanceName: name,
				PanelWSURL:   provision.PanelWSURL("http://panel.example.com"),
				AgentBase:    "https://dl.example.com", AgentVersion: "1.0.0",
				NewToken:     func() (string, error) { return "tok-replace", nil },
			}
		},
	}
	node := storage.Node{Name: "entry-4", Port: 443, Protocol: "vless"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatalf("L3 run: %v", err)
	}
	var got storage.Node
	if err := db.Where("name = ?", "entry-4-r").First(&got).Error; err != nil {
		t.Fatalf("replacement node: %v", err)
	}
	if got.Status != "provisioning" || got.Token != "tok-replace" {
		t.Fatalf("replacement = %+v", got)
	}
	var jobs []storage.ProvisionJob
	if err := db.Find(&jobs).Error; err != nil || len(jobs) != 1 || jobs[0].Status != "ok" {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}

	// 未配置模板：失败留原因，不冒称支持。
	bare := &InstanceAction{Manager: m, Store: store}
	if err := bare.Run(context.Background(), node); err == nil {
		t.Fatal("unconfigured L3 must fail")
	}
}
