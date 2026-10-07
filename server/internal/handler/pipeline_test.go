package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
)

// TestProvisionPipeline 覆盖入池流水线（OS-4）：provisioning 节点首连——
// 注册地址补全、元数据补空白（meta_init 下不覆盖面板值）、模板配置自动
// 下发（applied）→ 进程报表 running「探测通过」→ online 且注记清空；
// 上下线不覆盖 provisioning 态（心跳不提前转 online）。
func TestProvisionPipeline(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	node := storage.Node{
		Name: "auto-1", Port: 443, Protocol: "vless", Enabled: true,
		Token: "tok-pipeline", Status: "provisioning",
		Config: `{"inbounds":[]}`, MetaInit: true,
		Role: "entry", Direction: "out", Region: "hkg", City: "",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(r)
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
		Token: "tok-pipeline", AgentID: "auto-1",
		Meta: agentproto.NodeMeta{City: "Hong Kong", Datacenter: "vultr-hkg"},
	})
	if err := c.WriteJSON(hello); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}

	// 首连后：provisioning 不被上下线覆盖；地址补注册 IP；空白元数据补全。
	var row storage.Node
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := db.Where("id = ?", node.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.Address != "" && row.City != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pipeline fill missing: address=%q city=%q", row.Address, row.City)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row.Status != "provisioning" {
		t.Fatalf("status after hello = %q, want provisioning", row.Status)
	}
	if row.City != "Hong Kong" || row.Datacenter != "vultr-hkg" {
		t.Fatalf("meta fill = %q/%q", row.City, row.Datacenter)
	}
	if row.Region != "hkg" || row.Role != "entry" {
		t.Fatalf("panel-owned meta must stay: %+v", row)
	}

	// 模板配置自动下发：面板侧推 config_push，agent 回 ack。
	push := readEnv(t, c)
	if push.Type != agentproto.MsgConfigPush {
		t.Fatalf("expected config_push, got %s", push.Type)
	}
	var cp agentproto.ConfigPush
	if err := push.Decode(&cp); err != nil {
		t.Fatal(err)
	}
	if cp.Proc != "xray" || cp.Payload != `{"inbounds":[]}` {
		t.Fatalf("config_push = %+v", cp)
	}
	ack, _ := agentproto.NewEnvelope(push.ID, agentproto.MsgConfigAck, agentproto.ConfigAck{
		Proc: cp.Proc, Version: cp.Version, OK: true, Validated: true,
	})
	if err := c.WriteJSON(ack); err != nil {
		t.Fatal(err)
	}

	// 心跳不提前转 online（探测通过前保持 provisioning）。
	hb, _ := agentproto.NewEnvelope("b1", agentproto.MsgHeartbeat, agentproto.Heartbeat{})
	if err := c.WriteJSON(hb); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, c); env.Type != agentproto.MsgHeartbeatAck {
		t.Fatalf("expected heartbeat_ack, got %s", env.Type)
	}
	if err := db.Where("id = ?", node.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "provisioning" {
		t.Fatalf("heartbeat must not flip provisioning: %q", row.Status)
	}

	// 进程报表 xray running + 配置 applied → 探测通过转 online，注记清空。
	pr, _ := agentproto.NewEnvelope("p1", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{{Name: "xray", State: "running"}},
	})
	if err := c.WriteJSON(pr); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err := db.Where("id = ?", node.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == "online" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe pass never flipped online: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row.ProvisionNote != "" {
		t.Fatalf("note must clear on online: %q", row.ProvisionNote)
	}
	var cfg storage.NodeConfig
	if err := db.Where("node_id = ?", node.ID).First(&cfg).Error; err != nil {
		t.Fatalf("config snapshot missing: %v", err)
	}
	if cfg.Status != "applied" || cfg.Proc != "xray" {
		t.Fatalf("node_config = %+v", cfg)
	}

	// 无配置模板的 provisioning 节点：不构成探测通过，保持原态并留注记。
	bare := storage.Node{Name: "auto-2", Port: 443, Protocol: "vless",
		Enabled: true, Token: "tok-bare", Status: "provisioning", MetaInit: true}
	if err := db.Create(&bare).Error; err != nil {
		t.Fatal(err)
	}
	c2, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/agent/ws", nil)
	if err != nil {
		t.Fatalf("dial bare: %v", err)
	}
	defer c2.Close()
	hello2, _ := agentproto.NewEnvelope("h2", agentproto.MsgHello, agentproto.Hello{Token: "tok-bare", AgentID: "auto-2"})
	if err := c2.WriteJSON(hello2); err != nil {
		t.Fatal(err)
	}
	if env := readEnv(t, c2); env.Type != agentproto.MsgHelloAck {
		t.Fatalf("expected hello_ack, got %s", env.Type)
	}
	pr2, _ := agentproto.NewEnvelope("p2", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{{Name: "xray", State: "running"}},
	})
	if err := c2.WriteJSON(pr2); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		row = storage.Node{} // 清零：复用 dest 结构会残留主键变 WHERE 条件
		if err := db.Where("id = ?", bare.ID).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.ProvisionNote != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bare node note missing: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row.Status != "provisioning" {
		t.Fatalf("bare node must stay provisioning: %q", row.Status)
	}
	if !strings.Contains(row.ProvisionNote, "配置模板") {
		t.Fatalf("bare note = %q", row.ProvisionNote)
	}
}
