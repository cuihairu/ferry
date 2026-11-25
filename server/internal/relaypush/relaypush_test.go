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

// TestRenderOverride covers landing override spec rendering: address = address:port,
// ws-tls passes the plugin name through, the rest take the default (the field is omitted), and an empty address is rejected.
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

// TestRepoint covers re-pointing pushes: offline also leaves a failed trail (the retry basis);
// manual wins over auto; with no effective assignment it does nothing.
func TestRepoint(t *testing.T) {
	db := newTestDB(t)
	p := New(db, agenthub.New(), nil) // empty hub: everything offline

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

	// No assignment: do nothing.
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

	// auto points at land-a: offline leaves a failed trail, and the payload is the rendered override spec.
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

	// Attach a newer auto row pointing at land-b: the newest auto is taken.
	seedAssign(landB.ID, alloc.PolicyLeastConn)
	if err := p.Repoint(entry.ID); !errors.Is(err, agenthub.ErrOffline) {
		t.Fatalf("repoint offline err = %v", err)
	}
	row = storage.NodeConfig{} // reuse the struct: a primary key left in would turn it into a query condition, so zero it first
	db.Where("node_id = ?", entry.ID).Order("id DESC").First(&row)
	if row.Payload != `{"landing_addr":"b.example.com:8443","tunnel":"ws-tls"}` {
		t.Fatalf("payload (latest auto) = %s", row.Payload)
	}

	// An earlier manual row wins over every auto: manual points back at land-a.
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

// TestOnSwitchDedup covers the line-switch callback: many events for the same entry push only once.
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

// ackWriter impersonates an agent connection: on receiving config.push it replies with a same-ID answer per reply.
// A nil reply means no answer (triggering the timeout branch).
type ackWriter struct {
	mu       sync.Mutex
	got      []agentproto.Envelope
	hub      *agenthub.Hub
	nodeID   int64
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
	w.hub.Deliver(w.nodeID, ack)
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

// onlineHub brings up one online connection; with a nil reply it does not answer.
func onlineHub(t *testing.T, nodeID uint, reply func(id string) (agentproto.Envelope, bool)) (*agenthub.Hub, *ackWriter) {
	t.Helper()
	hub := agenthub.New()
	w := &ackWriter{hub: hub, reply: reply, nodeID: int64(nodeID)}
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

// TestPushAckApplied online and ack.OK: applied is persisted, 0 is returned, and the payload matches the digest.
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

// TestPushAckRejected ack.OK=false is a business failure: a failed trail is left but a nil err is returned.
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

// TestPushBadAckDecode an undecodable answer payload: a 502 trail is left.
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

// TestPushTimeout online but no answer: ErrTimeout -> a 504 trail (with the window shortened).
func TestPushTimeout(t *testing.T) {
	old := ackTimeout
	ackTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ackTimeout = old })

	db := newTestDB(t)
	hub, _ := onlineHub(t, 7, nil) // reply=nil: no answer
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{}`)
	if !errors.Is(err, agenthub.ErrTimeout) || code != 504 {
		t.Fatalf("Push = %d, %v; want 504 ErrTimeout", code, err)
	}
	if row.Status != "failed" {
		t.Fatalf("row = %+v; want failed", row)
	}
}

// TestPushSendError a closed connection: the write failure leaves a 502 trail.
func TestPushSendError(t *testing.T) {
	db := newTestDB(t)
	hub, w := onlineHub(t, 7, nil)
	conn := agenthub.NewConn(7, w)
	hub.Register(conn)
	conn.Close() // already closed underneath: Send gives ErrOffline directly
	p := New(db, hub, nil)

	row, code, err := p.Push(7, "relay", "relay", `{}`)
	if !errors.Is(err, agenthub.ErrOffline) || code != 502 {
		t.Fatalf("Push = %d, %v; want 502 ErrOffline", code, err)
	}
	if row.Status != "failed" {
		t.Fatalf("row = %+v; want failed", row)
	}
}

// TestRepointLandingErrors covers landing-side anomalies of re-pointing: a missing landing node and an empty address both return an error directly, without pushing.
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

	// The assignment points at a landing that no longer exists: the DB lookup failure returns immediately.
	seed(999)
	if err := p.Repoint(entry.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("repoint missing landing = %v; want record not found", err)
	}
	// The landing address is empty: the render failure returns immediately.
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
	// Neither failure should leave a push trail behind.
	var n int64
	db.Model(&storage.NodeConfig{}).Where("node_id = ?", entry.ID).Count(&n)
	if n != 0 {
		t.Fatalf("node configs after landing errors = %d; want 0", n)
	}
}
