package relaypush

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/alloc"
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

// TestRenderOverride 覆盖落地覆盖 spec 渲染：地址=address:port，
// ws-tls 透传插件名，其余走缺省（字段省略），空地址拒绝。
func TestRenderOverride(t *testing.T) {
	ov, err := RenderOverride(storage.Node{Address: "hk.example.com", Port: 443, Transport: "tls"})
	if err != nil {
		t.Fatal(err)
	}
	if ov != `{"landing_addr":"hk.example.com:443"}` {
		t.Fatalf("tls override = %s", ov)
	}
	ov, err = RenderOverride(storage.Node{Address: "10.0.0.2", Port: 8443, Transport: "ws-tls"})
	if err != nil {
		t.Fatal(err)
	}
	if ov != `{"landing_addr":"10.0.0.2:8443","tunnel":"ws-tls"}` {
		t.Fatalf("ws-tls override = %s", ov)
	}
	if _, err := RenderOverride(storage.Node{Port: 443}); err == nil {
		t.Fatal("empty address must fail")
	}
}

// TestRepoint 覆盖重指下发：offline 也留痕 failed（重试依据）；
// manual 优先于 auto；无生效分配不动作。
func TestRepoint(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil) // 空 hub：全部离线

	entry := storage.Node{Name: "entry-x", Token: "t1", Role: "entry", Enabled: true}
	landA := storage.Node{Name: "land-a", Token: "t2", Role: "landing", Enabled: true,
		Address: "a.example.com", Port: 443, Transport: "tls"}
	landB := storage.Node{Name: "land-b", Token: "t3", Role: "landing", Enabled: true,
		Address: "b.example.com", Port: 8443, Transport: "ws-tls"}
	for _, n := range []*storage.Node{&entry, &landA, &landB} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 无分配：不动作。
	if err := p.Repoint(entry.ID); err != nil {
		t.Fatalf("repoint without assignment: %v", err)
	}
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 0 {
		t.Fatalf("node configs without assignment = %d", n)
	}

	seedAssign := func(landing uint, strategy string) {
		t.Helper()
		if err := db.Create(&storage.LandingAssignment{
			EntryNodeID: &entry.ID, LandingNodeID: landing, Strategy: strategy,
			Direction: "out", AssignedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// auto 指向 land-a：offline 留痕 failed，payload 为渲染出的覆盖 spec。
	seedAssign(landA.ID, alloc.PolicyCostFirst)
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	var row storage.NodeConfig
	if err := db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Proc != "relay" || row.Kind != "relay" || row.Status != "failed" {
		t.Fatalf("config row = %+v", row)
	}
	if row.Payload != `{"landing_addr":"a.example.com:443"}` {
		t.Fatalf("payload = %s", row.Payload)
	}

	// 再挂一条更新的 auto 行指向 land-b：取最新 auto。
	seedAssign(landB.ID, alloc.PolicyLeastConn)
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	row = storage.NodeConfig{} // 复用结构体带主键会变查询条件，先清零
	db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row)
	if row.Payload != `{"landing_addr":"b.example.com:8443","tunnel":"ws-tls"}` {
		t.Fatalf("payload (latest auto) = %s", row.Payload)
	}

	// 更早的 manual 行优先于一切 auto：manual 指回 land-a。
	manual := storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landA.ID, Strategy: alloc.PolicyManual,
		Direction: "out", AssignedAt: time.Now().Add(-time.Hour),
	}
	if err := db.Create(&manual).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	row = storage.NodeConfig{}
	db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row)
	if row.Payload != `{"landing_addr":"a.example.com:443"}` {
		t.Fatalf("payload (manual wins) = %s", row.Payload)
	}
}

// TestOnSwitchDedup 覆盖换线回调：同一入口多事件只推一次。
func TestOnSwitchDedup(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil)

	entry := storage.Node{Name: "entry-x", Token: "t1", Role: "entry", Enabled: true}
	land := storage.Node{Name: "land-x", Token: "t2", Role: "landing", Enabled: true,
		Address: "x.example.com", Port: 443, Transport: "tls"}
	for _, n := range []*storage.Node{&entry, &land} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: land.ID, Strategy: alloc.PolicyCostFirst,
		Direction: "out", AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	p.OnSwitch([]alloc.SwitchEvent{
		{EntryID: entry.ID, EntryName: entry.Name, ToName: "land-b"},
		{EntryID: entry.ID, EntryName: entry.Name, ToName: "land-c"},
	})
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 1 {
		t.Fatalf("node configs after dedup = %d", n)
	}
}

// ackWriter 冒充 agent 连接：收到 config.push 即按 reply 回一条同 ID 应答。
// reply 传 nil 表示不回应（触发超时分支）。
type ackWriter struct {
	mu       sync.Mutex
	got      []agentproto.Envelope
	hub      *agenthub.Hub
	reply    func(id string) (agentproto.Envelope, bool)
	failWith error
}

func (w *ackWriter) WriteMessage(_ int, data []byte) error {
	if w.failWith != nil {
		return w.failWith
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	w.mu.Lock()
	w.got = append(w.got, env)
	w.mu.Unlock()
	if w.reply == nil {
		return nil
	}
	ack, ok := w.reply(env.ID)
	if !ok {
		return nil
	}
	w.hub.Deliver(ack)
	return nil
}

func (w *ackWriter) last(t *testing.T) agentproto.Envelope {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.got) == 0 {
		t.Fatal("no config push captured")
	}
	return w.got[len(w.got)-1]
}

// onlineHub 起一条在线连接；reply 为 nil 时不回包。
func onlineHub(t *testing.T, nodeID uint, reply func(id string) (agentproto.Envelope, bool)) (*agenthub.Hub, *ackWriter) {
	t.Helper()
	hub := agenthub.New()
	w := &ackWriter{hub: hub, reply: reply}
	hub.Register(agenthub.NewConn(int64(nodeID), w))
	t.Cleanup(func() { hub.Unregister(int64(nodeID), nil) })
	return hub, w
}

func ackEnv(t *testing.T, id string, ack agentproto.ConfigAck) agentproto.Envelope {
	t.Helper()
	env, err := agentproto.NewEnvelope(id, agentproto.MsgConfigAck, ack)
	if err != nil {
		t.Fatalf("build ack: %v", err)
	}
	return env
}

func lastConfig(t *testing.T, db *gorm.DB, nodeID uint) storage.NodeConfig {
	t.Helper()
	var row storage.NodeConfig
	if err := db.Where("node_id = ?", nodeID).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("get node config: %v", err)
	}
	return row
}

// TestPushAckApplied 在线且 ack.OK：落库 applied、返回 0、载荷与摘要一致。
func TestPushAckApplied(t *testing.T) {
	db := newTestDB(t)
	hub, w := onlineHub(t, 7, func(id string) (agentproto.Envelope, bool) {
		return ackEnv(t, id, agentproto.ConfigAck{Proc: "relay", OK: true, Validated: true}), true
	})
	p := New(db, hub, nil)

	payload := `{"landing_addr":"a.example.com:443"}`
	row, code, err := p.Push(7, "relay", "relay", payload)
	if err != nil || code != 0 {
		t.Fatalf("Push = %d, %v; want 0, nil", code, err)
	}
	if row.Status != "applied" || !row.Validated || row.Reverted {
		t.Fatalf("row = %+v; want applied+validated", row)
	}
	var push agentproto.ConfigPush
	if err := w.last(t).Decode(&push); err != nil {
		t.Fatalf("decode push: %v", err)
	}
	if push.Proc != "relay" || push.Payload != payload || push.Version != row.Sha256 {
		t.Fatalf("push = %+v; payload/digest mismatch with row %+v", push, row)
	}
	if stored := lastConfig(t, db, 7); stored.Status != "applied" {
		t.Fatalf("stored status = %q; want applied", stored.Status)
	}
}

// TestPushAckRejected ack.OK=false 属业务失败：留痕 failed 但按 nil err 返回。
func TestPushAckRejected(t *testing.T) {
	db := newTestDB(t)
	hub, _ := onlineHub(t, 7, func(id string) (agentproto.Envelope, bool) {
		return ackEnv(t, id, agentproto.ConfigAck{Proc: "relay", OK: false, Reverted: true, Error: "bad json"}), true
	})
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{"landing_addr":"a:443"}`)
	if err != nil || code != 0 {
		t.Fatalf("Push = %d, %v; business failure should be 0, nil", code, err)
	}
	if row.Status != "failed" || !row.Reverted || row.Error != "bad json" {
		t.Fatalf("row = %+v; want failed+reverted+error", row)
	}
}

// TestPushBadAckDecode 应答载荷解不开：按 502 留痕。
func TestPushBadAckDecode(t *testing.T) {
	db := newTestDB(t)
	hub, _ := onlineHub(t, 7, func(id string) (agentproto.Envelope, bool) {
		env, err := agentproto.NewEnvelope(id, agentproto.MsgConfigAck, "junk")
		if err != nil {
			t.Fatalf("build bad ack: %v", err)
		}
		return env, true
	})
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{}`)
	if err == nil || code != 502 {
		t.Fatalf("Push = %d, %v; want 502 with error", code, err)
	}
	if row.Status != "failed" || len(row.Error) < len("bad config_ack: ") ||
		row.Error[:len("bad config_ack: ")] != "bad config_ack: " {
		t.Fatalf("row = %+v; want failed with bad config_ack prefix", row)
	}
}

// TestPushTimeout 在线不回包：ErrTimeout → 504 留痕（缩短窗口跑）。
func TestPushTimeout(t *testing.T) {
	old := ackTimeout
	ackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ackTimeout = old })

	db := newTestDB(t)
	hub, _ := onlineHub(t, 7, nil) // reply=nil：不回应
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{}`)
	if !errors.Is(err, agenthub.ErrTimeout) || code != 504 {
		t.Fatalf("Push = %d, %v; want 504 ErrTimeout", code, err)
	}
	if row.Status != "failed" {
		t.Fatalf("row = %+v; want failed", row)
	}
}

// TestPushSendError 连接已关闭：写失败按 502 留痕。
func TestPushSendError(t *testing.T) {
	db := newTestDB(t)
	hub, w := onlineHub(t, 7, nil)
	conn := agenthub.NewConn(7, w)
	hub.Register(conn)
	conn.Close() // 底层已关：Send 直接 ErrOffline
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{}`)
	if !errors.Is(err, agenthub.ErrOffline) || code != 502 {
		t.Fatalf("Push = %d, %v; want 502 ErrOffline", code, err)
	}
	if row.Status != "failed" {
		t.Fatalf("row = %+v; want failed", row)
	}
}

// TestRepointLandingErrors 覆盖重指的落地侧异常：落地节点缺失与地址为空都直接返回错误，不下发。
func TestRepointLandingErrors(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil)

	entry := storage.Node{Name: "entry-x", Token: "t1", Role: "entry", Enabled: true}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	seed := func(landingID uint) {
		t.Helper()
		if err := db.Create(&storage.LandingAssignment{
			EntryNodeID: &entry.ID, LandingNodeID: landingID, Strategy: alloc.PolicyCostFirst,
			Direction: "out", AssignedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 分配指向已不存在的落地：查库失败即返回。
	seed(999)
	if err := p.Repoint(entry.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("repoint missing landing = %v; want record not found", err)
	}
	// 落地地址为空：渲染失败即返回。
	noAddr := storage.Node{Name: "land-y", Token: "t3", Role: "landing", Enabled: true, Port: 443}
	if err := db.Create(&noAddr).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.LandingAssignment{}).
		Where("entry_node_id = ?", entry.ID).Update("landing_node_id", noAddr.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.Repoint(entry.ID); err == nil {
		t.Fatal("repoint with empty landing address should fail")
	}
	// 两种失败都不应留下下发留痕。
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 0 {
		t.Fatalf("node configs after landing errors = %d; want 0", n)
	}
}
