// Package relaypush 把落地分配变更渲染成 relay 落地覆盖 spec 并经
// config.push 下发到入口 agent（E-16b 摘挂热更新换线）：agent 侧 relay
// 覆盖文件 SIGHUP 热重指，新连接走新落地、存量连接排空。
package relaypush

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// ackTimeout 覆盖 agent 侧校验命令 + reload 的最坏耗时（与配置下发同口径）。
const ackTimeout = 60 * time.Second

// Pusher 持有库与 agent 连接枢纽，把落地指向变更推到入口。
type Pusher struct {
	db  *gorm.DB
	hub *agenthub.Hub
	log *log.Logger
}

// New 创建推送器；logger 空取标准日志。
func New(db *gorm.DB, hub *agenthub.Hub, logger *log.Logger) *Pusher {
	if logger == nil {
		logger = log.Default()
	}
	return &Pusher{db: db, hub: hub, log: logger}
}

// Override 是 relay 落地覆盖 spec：只含面板权威的落地指向字段，
// listen 等本机参数沿用节点上的 agent 配置（agent 侧按非空字段合并）。
type Override struct {
	LandingAddr string `json:"landing_addr"`
	Tunnel      string `json:"tunnel,omitempty"`
}

// RenderOverride 由落地节点渲染覆盖 spec：地址=落地 address:port；
// 传输映射 ws-tls→ws-tls，其余（tls/quic/ssh/空）走缺省 tls-camo。
func RenderOverride(landing storage.Node) (string, error) {
	if landing.Address == "" {
		return "", errors.New("landing address is empty")
	}
	ov := Override{LandingAddr: fmt.Sprintf("%s:%d", landing.Address, landing.Port)}
	if landing.Transport == "ws-tls" {
		ov.Tunnel = "ws-tls"
	}
	b, err := json.Marshal(ov)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Push 下发一份节点配置并按 ack 留痕（与 handler 的手动配置下发同链路）：
// 落库 pending → agenthub 等待 config.ack → 按结果置 applied/failed。
// 返回 err != nil 时 status 为对应 HTTP 语义码；agent ack 的 OK=false
// 属业务失败，留痕后按 nil err 返回。
func (p *Pusher) Push(nodeID uint, proc, kind, payload string) (storage.NodeConfig, int, error) {
	sum := sha256.Sum256([]byte(payload))
	digest := hex.EncodeToString(sum[:])
	row := storage.NodeConfig{
		NodeID: nodeID, Proc: proc, Kind: kind,
		Version: digest, Sha256: digest, Payload: payload,
		Status: "pending",
	}
	if err := p.db.Create(&row).Error; err != nil {
		return row, 500, err
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("cfg-%d-%d", nodeID, time.Now().UnixNano()),
		agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: proc, Kind: kind, Version: digest, Sha256: digest, Payload: payload},
	)
	if err != nil {
		return row, 500, err
	}
	reply, err := p.hub.Request(int64(nodeID), env, ackTimeout)
	if err != nil {
		p.Finish(&row, "failed", false, false, err.Error())
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			return row, 502, err
		case errors.Is(err, agenthub.ErrTimeout):
			return row, 504, err
		default:
			return row, 500, err
		}
	}
	var ack agentproto.ConfigAck
	if err := reply.Decode(&ack); err != nil {
		p.Finish(&row, "failed", false, false, "bad config_ack: "+err.Error())
		return row, 502, err
	}
	status := "failed"
	if ack.OK {
		status = "applied"
	}
	p.Finish(&row, status, ack.Reverted, ack.Validated, ack.Error)
	return row, 0, nil
}

// finish 把下发结果写回留痕记录；失败仅记日志（记录本身已可追溯）。
func (p *Pusher) Finish(row *storage.NodeConfig, status string, reverted, validated bool, errMsg string) {
	updates := map[string]any{
		"status": status, "reverted": reverted, "validated": validated, "error": errMsg,
	}
	if err := p.db.Model(&storage.NodeConfig{}).Where("id=?", row.ID).Updates(updates).Error; err != nil {
		p.log.Printf("relaypush: update node_config id=%d: %v", row.ID, err)
		return
	}
	row.Status, row.Reverted, row.Validated, row.Error = status, reverted, validated, errMsg
}

// OnSwitch 是 alloc 换线回调（E-16b）：变更列表去重入口，逐个重指现行落地。
// 单入口失败只记日志，不中断其余入口。
func (p *Pusher) OnSwitch(events []alloc.SwitchEvent) {
	seen := map[uint]bool{}
	for _, ev := range events {
		if seen[ev.EntryID] {
			continue
		}
		seen[ev.EntryID] = true
		if err := p.Repoint(ev.EntryID); err != nil {
			p.log.Printf("relaypush: repoint entry %d (%s): %v", ev.EntryID, ev.EntryName, err)
		}
	}
}

// Repoint 把入口现行生效分配（manual 优先，其次最新 auto）渲染为 relay
// 覆盖 spec 并下发；无生效分配不动作。agent 离线/超时留痕 failed 等重试。
func (p *Pusher) Repoint(entryID uint) error {
	var row storage.LandingAssignment
	err := p.db.Where("entry_node_id = ? AND released_at IS NULL", entryID).
		Order("CASE WHEN strategy = 'manual' THEN 0 ELSE 1 END, id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // 入口没挂分配，无需重指
	}
	if err != nil {
		return err
	}
	var landing storage.Node
	if err := p.db.First(&landing, row.LandingNodeID).Error; err != nil {
		return err
	}
	payload, err := RenderOverride(landing)
	if err != nil {
		return err
	}
	_, _, err = p.Push(entryID, "relay", "relay", payload)
	return err
}
