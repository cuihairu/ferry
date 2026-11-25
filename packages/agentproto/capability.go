package agentproto

// Capability names (F-0222~F-0227): what a node is able to serve. The static
// set is declared at hello (or derived from the role), the dynamic state rides
// each heartbeat. These constants are the wire contract shared by the agent
// and the panel.
const (
	CapProxyIngress = "proxy_ingress" // serves subscription client ingress (proxy cores running)
	CapRelayEntry   = "relay_entry"   // runs the relay data plane toward a landing
	CapRelayLanding = "relay_landing" // terminates the relay data plane
	CapProbe        = "probe"         // runs edge probe tasks
	CapTCPForward   = "tcp_forward"   // TCP forwarding capability
	CapUDPForward   = "udp_forward"   // UDP forwarding capability
)

// Dynamic capability state values (F-0229); an absent key means "unknown".
const (
	CapStateOK       = "ok"
	CapStateDegraded = "degraded"
	CapStateOff      = "off"
)

// KnownCapabilities is the accepted capability name set (validation).
var KnownCapabilities = map[string]bool{
	CapProxyIngress: true, CapRelayEntry: true, CapRelayLanding: true,
	CapProbe: true, CapTCPForward: true, CapUDPForward: true,
}

// KnownCapState reports whether s is a legal dynamic state value.
func KnownCapState(s string) bool {
	return s == CapStateOK || s == CapStateDegraded || s == CapStateOff
}

// DeriveCapsFromMeta is the role-derived capability fallback (F-0222): what a
// node of this role is expected to serve when neither the panel nor the agent
// declared an explicit set.
func DeriveCapsFromMeta(meta NodeMeta) []string {
	out := []string{}
	if meta.Role == RoleEntry || meta.Role == RoleBoth {
		out = append(out, CapProxyIngress, CapRelayEntry, CapProbe)
	}
	if meta.Role == RoleLanding || meta.Role == RoleBoth {
		out = append(out, CapRelayLanding)
	}
	return out
}

// ValidCaps validates a capability name list: unknown names are rejected and
// duplicates removed, order preserved. Returns the clean list.
func ValidCaps(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range in {
		if !KnownCapabilities[c] {
			return nil, ErrUnknownCapability{c}
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// ErrUnknownCapability rejects a capability name outside the known set.
type ErrUnknownCapability struct{ Name string }

func (e ErrUnknownCapability) Error() string { return "unknown capability " + e.Name }
