// Node provisioning pipeline (OS-4): automated completion for provisioning nodes.
// Flow: template config push (same config.push channel as manual push, validated
// and persisted agent-side) -> probe pass (that proc's config applied + agent
// reports proc running) -> online. Nodes that cannot advance keep a provision_note
// reason; up/down transitions never overwrite the provisioning state (agentws).
package handler

import (
	"log"
	"strings"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// pipelineDeploy auto-pushes the template config: only when the node is
// provisioning, the config template is non-empty, and no applied push exists
// yet (reconnect neither re-pushes nor refreshes the snapshot); an empty
// template or a failed push leaves a note. Blocks waiting for the agent ack
// (same convention as config.push); callers must invoke it from a goroutine.
func (h *Handler) pipelineDeploy(nodeID uint) {
	var n storage.Node
	if err := h.db.First(&n, nodeID).Error; err != nil || n.Status != "provisioning" {
		return
	}
	if strings.TrimSpace(n.Config) == "" || n.Config == "{}" {
		h.setProvisionNote(nodeID, "等待协议配置模板：节点页下发配置后自动入池")
		return
	}
	var applied int64
	h.db.Model(&storage.NodeConfig{}).Where("node_id = ? AND status = ?", nodeID, "applied").Count(&applied)
	if applied > 0 {
		return
	}
	proc, kind := procForProtocol(n.Protocol)
	row, _, err := h.pusher.Push(nodeID, proc, kind, n.Config)
	if err != nil {
		h.setProvisionNote(nodeID, "配置下发失败: "+err.Error())
		return
	}
	if row.Status != "applied" {
		h.setProvisionNote(nodeID, "配置下发未生效: "+row.Error)
	}
}

// procForProtocol maps a protocol to the managed kernel process (data-driven
// since the protocol pluginization: the registry's KernelProc/KernelKind;
// the agent's procs[].name must match to receive a push). Protocols without a
// declared kernel fall back to xray (node creation validation already blocks
// those; this is defense in depth only).
func procForProtocol(name string) (proc, kind string) {
	if p, err := protocol.Lookup(name); err == nil && p.KernelProc != "" {
		return p.KernelProc, p.KernelKind
	}
	return "xray", "xray"
}

// pipelineProbePass marks a probe pass: a provisioning node reports its target
// proc running and that proc's config is applied (serving surface ready), so it
// goes online into the pool and its note is cleared. Any other state writes nothing.
func (h *Handler) pipelineProbePass(nodeID uint, proc string) {
	var applied int64
	h.db.Model(&storage.NodeConfig{}).Where("node_id = ? AND proc = ? AND status = ?", nodeID, proc, "applied").Count(&applied)
	if applied == 0 {
		return
	}
	res := h.db.Model(&storage.Node{}).Where("id = ? AND status = ?", nodeID, "provisioning").
		Updates(map[string]any{"status": "online", "provision_note": ""})
	if res.RowsAffected > 0 {
		log.Printf("node provision pipeline done: node=%d proc=%s -> online", nodeID, proc)
	}
}

// setProvisionNote writes a progress/failure note only while the node is still
// provisioning (never overwrites once it went online).
func (h *Handler) setProvisionNote(nodeID uint, note string) {
	h.db.Model(&storage.Node{}).Where("id = ? AND status = ?", nodeID, "provisioning").
		Update("provision_note", note)
}
