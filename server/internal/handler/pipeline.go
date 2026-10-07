// 入池流水线（OS-4）：provisioning 节点的自动化收尾。
// 口径：模板配置下发（与手动下发同一 config.push 通道，agent 侧校验落盘）
// → 探测通过（该进程配置已 applied + agent 进程报表 running）→ online 入池；
// 推进不动的留 provision_note 原因，上下线不覆盖 provisioning 态（agentws）。
package handler

import (
	"log"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// pipelineDeploy 模板配置自动下发：provisioning + 配置模板非空 + 尚无
// applied 下发才推送（重连不重推、不刷快照）；模板为空/推送失败留注记。
// 阻塞等待 agent ack（与 config.push 同口径），调用方须在 goroutine 触发。
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
	row, _, err := h.pusher.Push(nodeID, "xray", "xray", n.Config)
	if err != nil {
		h.setProvisionNote(nodeID, "配置下发失败: "+err.Error())
		return
	}
	if row.Status != "applied" {
		h.setProvisionNote(nodeID, "配置下发未生效: "+row.Error)
	}
}

// pipelineProbePass 探测通过：provisioning 节点目标进程 running 上报，且
// 该进程配置已 applied（服务面就绪），转 online 入池并清注记。其余状态零写入。
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

// setProvisionNote 只在节点仍处供给态时写进度/失败注记（转 online 后不覆盖）。
func (h *Handler) setProvisionNote(nodeID uint, note string) {
	h.db.Model(&storage.Node{}).Where("id = ? AND status = ?", nodeID, "provisioning").
		Update("provision_note", note)
}
