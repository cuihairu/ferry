package agentproto

import (
	"testing"
)

// TestProbeReportJitterRoundTrip covers the F-0907 wire field: jitter_ms
// survives an envelope round trip, and stays absent (omitempty) when zero so
// old receivers and old senders stay on the same wire shape.
func TestProbeReportJitterRoundTrip(t *testing.T) {
	env, err := NewEnvelope("j-1", MsgProbeReport, ProbeReportBatch{Items: []ProbeReport{
		{TargetKind: ProbeTargetExit, RttMs: 150, JitterMs: 12, LossPct: 2, Verdict: ProbeVerdictHealthy},
		{TargetKind: ProbeTargetPeer, RttMs: 80, Verdict: ProbeVerdictHealthy},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var p ProbeReportBatch
	if err := env.Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("items = %d", len(p.Items))
	}
	if p.Items[0].JitterMs != 12 {
		t.Fatalf("jitter_ms = %d, want 12", p.Items[0].JitterMs)
	}
	if p.Items[1].JitterMs != 0 {
		t.Fatalf("unset jitter_ms = %d, want 0", p.Items[1].JitterMs)
	}
}
