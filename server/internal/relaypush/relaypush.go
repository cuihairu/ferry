// Package relaypush renders landing-assignment changes into a relay landing override spec and pushes it to
// the entry agent via config.push (E-16b hot-update line switching on attach/detach): on the agent side the relay
// override file is re-pointed by a SIGHUP hot reload, so new connections take the new landing while existing ones drain.
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

// ackTimeout covers the worst-case time of the agent-side validation command plus reload (same policy as config push).
// Declared as a variable so tests can shorten the window.
var ackTimeout = 60 * time.Second

// Pusher holds the DB and the agent-connection hub and pushes landing re-pointings to entries.
type Pusher struct {
	db  *gorm.DB
	hub *agenthub.Hub
	log *log.Logger
}

// New creates a pusher; a nil logger falls back to the standard logger.
func New(db *gorm.DB, hub *agenthub.Hub, logger *log.Logger) *Pusher {
	if logger == nil {
		logger = log.Default()
	}
	return &Pusher{db: db, hub: hub, log: logger}
}

// Override is the relay landing override spec: it contains only the panel-authoritative landing fields;
// local parameters such as listen keep the agent config on the node (the agent merges by non-empty fields).
type Override struct {
	LandingAddr string `json:"landing_addr"`
	Tunnel      string `json:"tunnel,omitempty"`
}

// RenderOverride renders the override spec from the landing node: address = the landing's address:port;
// the transport maps ws-tls -> ws-tls, everything else (tls/quic/ssh/empty) takes the default tls-camo.
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

// Push pushes a node config and leaves a trail by ack (the same path as the handler's manual config push):
// pending is persisted -> agenthub waits for config.ack -> applied/failed is set by the result.
// When err != nil, status is the corresponding HTTP semantic code; an agent ack with OK=false
// is a business failure, left in the trail and returned as a nil err.
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

// finish writes the push result back into the trail record; a failure only logs (the record itself is already traceable).
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

// OnSwitch is the alloc line-switch callback (E-16b): it dedups the entries in the change list and re-points each
// current landing. A single entry's failure only logs and does not interrupt the remaining entries.
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

// Repoint renders the entry's currently effective assignment (manual first, then the newest auto) into a relay
// override spec and pushes it; with no effective assignment it does nothing. An offline / timed-out agent leaves a failed trail to await a retry.
func (p *Pusher) Repoint(entryID uint) error {
	var row storage.LandingAssignment
	err := p.db.Where("entry_node_id = ? AND released_at IS NULL", entryID).
		Order("CASE WHEN strategy = 'manual' THEN 0 ELSE 1 END, id DESC").
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // the entry has no assignment, no re-pointing needed
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
