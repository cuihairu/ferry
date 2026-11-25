package handler

import (
	"log"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// syncCaps records the agent's statically declared capability set at hello
// (F-0228): the first non-empty report fills the empty stored list with a
// history row (F-0231); afterwards the panel's own list wins and a diverging
// report is only logged. An empty report (old agent) keeps the role-derived
// fallback computed by storage.StaticCaps.
func (h *Handler) syncCaps(nodeID int64, reported []string) {
	if len(reported) == 0 {
		return
	}
	var node storage.Node
	if err := h.db.Where("id=?", nodeID).First(&node).Error; err != nil {
		log.Printf("load node caps node=%d: %v", nodeID, err)
		return
	}
	// Unknown names are dropped rather than rejecting the whole report: a
	// newer agent talking to an older panel keeps its known subset.
	clean := []string{}
	for _, c := range reported {
		if !agentproto.KnownCapabilities[c] {
			log.Printf("ignore unknown cap node=%d cap=%q", nodeID, c)
			continue
		}
		dup := false
		for _, k := range clean {
			if k == c {
				dup = true
				break
			}
		}
		if !dup {
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return
	}
	before := node.Capabilities
	// The panel's stored list wins once set (explicitly, or by a prior agent
	// report); a legacy "" row is still fillable.
	if before != "" && before != "[]" {
		if before != storage.MustJSON(clean) {
			log.Printf("node caps differ from panel list node=%d: agent=%v panel=%s",
				nodeID, clean, before)
		}
		return
	}
	after := storage.MustJSON(clean)
	if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).
		Update("capabilities", after).Error; err != nil {
		log.Printf("store node caps node=%d: %v", nodeID, err)
		return
	}
	h.recordCapabilityChange(uint(nodeID), "static", "[]", after, "agent hello")
}

// mergeCaps folds the heartbeat-reported runtime capability state into the
// node row (F-0229) and appends a history row per capability whose state
// actually changed (F-0231). Unknown names or illegal state values are dropped;
// an empty report (old agent) is a no-op.
func (h *Handler) mergeCaps(nodeID int64, reported map[string]string) {
	if len(reported) == 0 {
		return
	}
	clean := map[string]string{}
	for k, v := range reported {
		if !agentproto.KnownCapabilities[k] || !agentproto.KnownCapState(v) {
			log.Printf("ignore invalid dynamic cap node=%d cap=%q state=%q", nodeID, k, v)
			continue
		}
		clean[k] = v
	}
	if len(clean) == 0 {
		return
	}
	var node storage.Node
	if err := h.db.Select("id", "capability_state").
		Where("id=?", nodeID).First(&node).Error; err != nil {
		log.Printf("load node dynamic caps node=%d: %v", nodeID, err)
		return
	}
	cur := storage.DynamicState(node)
	merged, changed := storage.MergeDynamic(cur, clean)
	raw := storage.JSONState(merged)
	if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).
		Update("capability_state", raw).Error; err != nil {
		log.Printf("store node dynamic caps node=%d: %v", nodeID, err)
		return
	}
	for _, c := range changed {
		h.recordCapabilityChange(uint(nodeID), "dynamic", cur[c], merged[c], "heartbeat")
	}
}

// recordCapabilityChange appends one capability transition (F-0231); a write
// failure only logs -- the current state has already been persisted. For
// kind=static Before/After carry the whole JSON list, for kind=dynamic the
// single capability's state value.
func (h *Handler) recordCapabilityChange(nodeID uint, kind, before, after, reason string) {
	change := storage.CapabilityChange{
		NodeID: nodeID,
		Kind:   kind,
		Before: before,
		After:  after,
		Reason: reason,
	}
	if err := h.db.Create(&change).Error; err != nil {
		log.Printf("record capability change node=%d: %v", nodeID, err)
	}
}
