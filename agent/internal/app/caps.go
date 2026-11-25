package app

import (
	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// staticCaps derives the node's statically declared capability set (F-0228)
// from the runtime config: what this agent is actually configured to run.
// Managed proxy cores mean client ingress, probe tasks mean the probe role,
// a relay spec means the relay entry plane, and the landing role terminates
// the relay plane. tcp_forward/udp_forward have no config source yet -- they
// are declared only (F-0227).
func staticCaps(cfg config.Config) []string {
	set := map[string]bool{}
	out := []string{}
	add := func(c string) {
		if !set[c] {
			set[c] = true
			out = append(out, c)
		}
	}
	if len(cfg.Procs) > 0 {
		add(agentproto.CapProxyIngress)
	}
	if len(cfg.Probes) > 0 {
		add(agentproto.CapProbe)
	}
	if cfg.Relay.Listen != "" {
		add(agentproto.CapRelayEntry)
	}
	if cfg.Meta.Role == agentproto.RoleLanding || cfg.Meta.Role == agentproto.RoleBoth {
		add(agentproto.CapRelayLanding)
	}
	return out
}

// dynamicCaps derives the runtime capability state (F-0229) from the live
// component snapshot taken with the heartbeat. Proxy ingress follows the
// managed process states: any crash degrades, all-down is off. Probe and
// relay run as standalone role processes outside this one, so their declared
// surfaces report ok while the core itself is alive (finer-grained signals
// come through the alarm channel, not the capability map).
func dynamicCaps(cfg config.Config, procs []agentproto.ProcStatus) map[string]string {
	out := map[string]string{}
	if len(cfg.Probes) > 0 {
		out[agentproto.CapProbe] = agentproto.CapStateOK
	}
	if cfg.Relay.Listen != "" {
		out[agentproto.CapRelayEntry] = agentproto.CapStateOK
	}
	if len(cfg.Procs) > 0 {
		running, down := 0, 0
		for _, p := range procs {
			if p.State == agentproto.ProcRunning {
				running++
			} else {
				down++
			}
		}
		switch {
		case running == 0:
			out[agentproto.CapProxyIngress] = agentproto.CapStateOff
		case down > 0:
			out[agentproto.CapProxyIngress] = agentproto.CapStateDegraded
		default:
			out[agentproto.CapProxyIngress] = agentproto.CapStateOK
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
