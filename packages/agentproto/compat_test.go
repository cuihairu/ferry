package agentproto

import (
	"encoding/json"
	"testing"
)

// decodeCompat is the cross-version decode helper: it parses a raw envelope as an
// old peer would send it and decodes the payload into the current struct.
func decodeCompat(t *testing.T, raw string) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("envelope decode: %v", err)
	}
	if !env.Valid() {
		t.Fatalf("envelope must pass the version guard: %+v", env)
	}
	return env
}

// TestOldAgentPayloadsDecode pins the backward-compat contract (F-0730): payloads
// sent by an older agent (fields added later absent) decode into the current
// structs with zero values, so the panel can be upgraded before the agents.
func TestOldAgentPayloadsDecode(t *testing.T) {
	// hello predating Caps (F-0228) and the cost fields.
	env := decodeCompat(t, `{"v":1,"id":"h1","type":"agent.hello","payload":{
		"token":"t","agent_id":"a1","version":"0.9",
		"meta":{"role":"landing","region":"unknown","isp":"unknown","transport":"tls","billing_type":"monthly","bw_up_mbps":10,"bw_down_mbps":10}
	}}`)
	var hello Hello
	if err := env.Decode(&hello); err != nil {
		t.Fatalf("old hello decode: %v", err)
	}
	if hello.Caps != nil || hello.Meta.MonthlyCostCents != 0 {
		t.Fatalf("late fields must default empty: %+v", hello)
	}

	// heartbeat predating the dynamic caps map (F-0229).
	env = decodeCompat(t, `{"v":1,"id":"b1","type":"agent.heartbeat","payload":{"uptime_sec":10,"at":"2026-01-01T00:00:00Z"}}`)
	var hb Heartbeat
	if err := env.Decode(&hb); err != nil {
		t.Fatalf("old heartbeat decode: %v", err)
	}
	if hb.Caps != nil {
		t.Fatalf("old heartbeat must carry no caps: %+v", hb)
	}

	// upgrade_status predating Error (the applied frame never carries one).
	env = decodeCompat(t, `{"v":1,"id":"u1-applied","type":"agent.upgrade_status","payload":{"upgrade_id":"u1","version":"v2","state":"applied"}}`)
	var us UpgradeStatus
	if err := env.Decode(&us); err != nil {
		t.Fatalf("old upgrade_status decode: %v", err)
	}
	if us.Error != "" {
		t.Fatalf("absent error must decode empty: %+v", us)
	}
}

// TestUnknownPayloadFieldsIgnored pins the forward-compat contract: a newer peer's
// extra fields are ignored on decode, so agents can upgrade before the panel.
func TestUnknownPayloadFieldsIgnored(t *testing.T) {
	env := decodeCompat(t, `{"v":1,"id":"h2","type":"agent.hello","payload":{
		"token":"t","agent_id":"a1","version":"9.9","future_field":{"x":1},
		"meta":{"role":"entry","region":"unknown","isp":"unknown","transport":"tls","billing_type":"monthly","bw_up_mbps":1,"bw_down_mbps":1,"future_meta":"y"}
	}}`)
	var hello Hello
	if err := env.Decode(&hello); err != nil {
		t.Fatalf("newer hello decode: %v", err)
	}
	if hello.AgentID != "a1" || hello.Meta.Role != "entry" {
		t.Fatalf("known fields must survive unknown siblings: %+v", hello)
	}
}

// TestUnknownTypeEnvelopeSurvivesDecode pins the mixed-version message rule: a type
// the old side does not know still decodes at the envelope layer (the consumer
// decides to drop it by type), so each end can upgrade independently.
func TestUnknownTypeEnvelopeSurvivesDecode(t *testing.T) {
	env := decodeCompat(t, `{"v":1,"id":"x1","type":"agent.some_future_message","payload":{"a":1}}`)
	if env.Type != "agent.some_future_message" {
		t.Fatalf("type must round trip: %s", env.Type)
	}
}
