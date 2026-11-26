package agentproto

import (
	"encoding/json"
	"testing"
)

// TestHeartbeatRelayCapsRoundTrip covers F-0804's wire contract: the structured
// declaration survives an envelope encode/decode, while an old heartbeat
// without the field decodes to nil (keep-on-absent on the server).
func TestHeartbeatRelayCapsRoundTrip(t *testing.T) {
	in := Heartbeat{
		UptimeSec: 10,
		RelayCaps: &RelayCaps{
			TCPForward:     true,
			Reconnect:      true,
			IdleCleanup:    true,
			Metrics:        true,
			StreamLimit:    true,
			BandwidthLimit: true,
			MaxConns:       64,
			MaxStreams:     256,
			RateLimitKbps:  2048,
		},
	}
	env, err := NewEnvelope("hb-1", MsgHeartbeat, in)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	var out Heartbeat
	if err := env.Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.RelayCaps == nil {
		t.Fatal("relay caps lost in round-trip")
	}
	if *out.RelayCaps != *in.RelayCaps {
		t.Fatalf("relay caps changed: %+v vs %+v", out.RelayCaps, in.RelayCaps)
	}

	old := `{"v":1,"id":"hb-2","type":"agent.heartbeat","payload":{"uptime_sec":10}}`
	var envOld Envelope
	if err := json.Unmarshal([]byte(old), &envOld); err != nil {
		t.Fatalf("old envelope: %v", err)
	}
	var outOld Heartbeat
	if err := envOld.Decode(&outOld); err != nil {
		t.Fatalf("old decode: %v", err)
	}
	if outOld.RelayCaps != nil {
		t.Fatalf("old heartbeat must decode to nil caps: %+v", outOld.RelayCaps)
	}
}
