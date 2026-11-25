package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/relaypush"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// provNode creates a provisioning node carrying a config template (pipeline input).
func provNode(t *testing.T, db *gorm.DB, name, token string) storage.Node {
	t.Helper()
	node := storage.Node{Name: name, Port: 443, Protocol: "vless", Enabled: true,
		Token: token, Status: "provisioning", Config: `{"inbounds":[]}`, MetaInit: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	return node
}

// waitForNote polls the provision note (the pipeline advances in a goroutine, so allow generous slack).
func waitForNote(t *testing.T, db *gorm.DB, id uint, want string) storage.Node {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var row storage.Node
		if err := db.Where("id = ?", id).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if strings.Contains(row.ProvisionNote, want) {
			return row
		}
		if time.Now().After(deadline) {
			t.Fatalf("note %q never showed, got %q", want, row.ProvisionNote)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestPipelineDeployAlreadyApplied covers returning immediately when an
// applied snapshot already exists: no new config push, no note written
// (a reconnect does not re-push).
func TestPipelineDeployAlreadyApplied(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	node := provNode(t, db, "auto-applied", "tok-applied")
	if err := db.Create(&storage.NodeConfig{NodeID: node.ID, Proc: "xray", Kind: "xray",
		Version: "v1", Sha256: "v1", Payload: `{"inbounds":[]}`, Status: "applied"}).Error; err != nil {
		t.Fatal(err)
	}

	(&Handler{db: db}).pipelineDeploy(node.ID)

	var n int64
	if err := db.Model(&storage.NodeConfig{}).Where("node_id = ?", node.ID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("config rows = %d, want 1", n)
	}
	var row storage.Node
	if err := db.Where("id = ?", node.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ProvisionNote != "" {
		t.Fatalf("note = %q, want empty", row.ProvisionNote)
	}
}

// TestPipelineDeployAgentOffline covers leaving a note on a failed push:
// with no agent online hub.Request returns ErrOffline immediately, the note
// records the config-push-failure marker, the snapshot row is marked failed,
// and the node stays provisioning.
func TestPipelineDeployAgentOffline(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	node := provNode(t, db, "auto-offline", "tok-offline")
	h := &Handler{db: db, pusher: relaypush.New(db, agenthub.New(), nil)}

	h.pipelineDeploy(node.ID)

	row := waitForNote(t, db, node.ID, "配置下发失败")
	if row.Status != "provisioning" {
		t.Fatalf("status = %q, want provisioning", row.Status)
	}
	var cfg storage.NodeConfig
	if err := db.Where("node_id = ?", node.ID).First(&cfg).Error; err != nil {
		t.Fatalf("pending snapshot missing: %v", err)
	}
	if cfg.Status != "failed" {
		t.Fatalf("snapshot status = %q, want failed", cfg.Status)
	}
}

// TestPipelineDeployAckNotApplied covers the agent replying OK=false: the
// config did not take effect, the note records the push-not-applied marker
// along with the agent's error text verbatim, and the snapshot row is marked
// failed.
func TestPipelineDeployAckNotApplied(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	node := provNode(t, db, "auto-badcfg", "tok-badcfg")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "auto-badcfg", "tok-badcfg")

	push := readEnv(t, c)
	if push.Type != agentproto.MsgConfigPush {
		t.Fatalf("expected config_push, got %s", push.Type)
	}
	var cp agentproto.ConfigPush
	if err := push.Decode(&cp); err != nil {
		t.Fatal(err)
	}
	replyAck(t, c, push, agentproto.MsgConfigAck, agentproto.ConfigAck{
		Proc: cp.Proc, Version: cp.Version, OK: false, Error: "invalid config",
	})

	row := waitForNote(t, db, node.ID, "配置下发未生效")
	if !strings.Contains(row.ProvisionNote, "invalid config") {
		t.Fatalf("note = %q, want agent error quoted", row.ProvisionNote)
	}
	var cfg storage.NodeConfig
	if err := db.Where("node_id = ?", node.ID).First(&cfg).Error; err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if cfg.Status != "failed" || cfg.Error != "invalid config" {
		t.Fatalf("snapshot = %+v", cfg)
	}
}
