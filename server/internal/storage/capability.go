package storage

import (
	"encoding/json"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// Capability names are the wire contract; the canonical definitions live in
// the shared agentproto package (F-0222). Re-exported here so the storage
// layer keeps its historical identifiers.
const (
	CapProxyIngress = agentproto.CapProxyIngress
	CapRelayEntry   = agentproto.CapRelayEntry
	CapRelayLanding = agentproto.CapRelayLanding
	CapProbe        = agentproto.CapProbe
	CapTCPForward   = agentproto.CapTCPForward
	CapUDPForward   = agentproto.CapUDPForward
)

// KnownCapabilities is the accepted capability name set (validation).
var KnownCapabilities = agentproto.KnownCapabilities

// StaticCaps returns the node's statically declared capabilities (F-0222):
// the panel-stored list when present, otherwise the role-derived default
// (entry/both: ingress+relay+probe, landing/both: relay_landing).
func StaticCaps(n Node) []string {
	if n.Capabilities != "" && n.Capabilities != "[]" {
		var out []string
		if err := json.Unmarshal([]byte(n.Capabilities), &out); err == nil {
			return out
		}
	}
	return agentproto.DeriveCapsFromMeta(agentproto.NodeMeta{Role: n.Role})
}

// DynamicState returns the latest agent-reported runtime capability state
// (F-0228); absent or corrupt state parses as empty (treated as unknown).
func DynamicState(n Node) map[string]string {
	if n.CapabilityState == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(n.CapabilityState), &out); err != nil {
		return nil
	}
	return out
}

// HasCapability is the scheduler gate (F-0230): the static list must cover the
// capability and the latest dynamic state must not mark it off. An empty
// dynamic state (old agents, first boot) does not disqualify.
func HasCapability(n Node, name string) bool {
	ok := false
	for _, c := range StaticCaps(n) {
		if c == name {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	if state := DynamicState(n)[name]; state == agentproto.CapStateOff {
		return false
	}
	return true
}

// ParseCaps validates and marshals a capability name list (F-0223~F-0227 naming
// contract); unknown names are rejected, duplicates removed.
func ParseCapsJSON(in []string) (string, error) {
	clean, err := agentproto.ValidCaps(in)
	if err != nil {
		return "", err
	}
	return MustJSON(clean), nil
}

// MustJSON marshals a capability list; the input is always valid, so the
// caller can ignore the error (test fixtures and derived defaults).
func MustJSON(in []string) string {
	raw, err := json.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// MergeDynamic folds agent-reported runtime states into the current state map
// (F-0229): reported entries override, absent entries keep their old value.
// Returns the merged map and the capability names whose state actually
// changed -- the history trigger (F-0231).
func MergeDynamic(cur map[string]string, reported map[string]string) (map[string]string, []string) {
	merged := make(map[string]string, len(cur)+len(reported))
	for k, v := range cur {
		merged[k] = v
	}
	changed := []string{}
	for k, v := range reported {
		if old, ok := merged[k]; ok && old == v {
			continue
		}
		merged[k] = v
		changed = append(changed, k)
	}
	return merged, changed
}

// JSONState marshals a dynamic state map; nil marshals as "" (no state stored).
func JSONState(in map[string]string) string {
	if len(in) == 0 {
		return ""
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return ""
	}
	return string(raw)
}

// CapabilityChange is the append-only history of capability transitions (F-0231):
// static set edits by the panel and dynamic state changes reported by agents.
type CapabilityChange struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"index" json:"node_id"`
	Kind      string    `json:"kind"` // static/dynamic
	Before    string    `json:"before"`
	After     string    `json:"after"`
	Reason    string    `json:"reason"` // "panel update", "agent hello", "heartbeat", etc.
	CreatedAt time.Time `json:"created_at"`
}
