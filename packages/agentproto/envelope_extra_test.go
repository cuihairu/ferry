package agentproto

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNewEnvelopeMarshalError covers an unserializable payload (a func value): NewEnvelope errors.
func TestNewEnvelopeMarshalError(t *testing.T) {
	if _, err := NewEnvelope("id-1", MsgProbeReport, func() {}); err == nil {
		t.Fatal("unmarshalable payload must error")
	}
}

// TestDecodeEmptyPayload covers decoding with an empty payload, which succeeds and leaves the target untouched.
func TestDecodeEmptyPayload(t *testing.T) {
	var p ProbeReportBatch
	if err := (Envelope{V: ProtocolVersion, Type: MsgProbeReport}).Decode(&p); err != nil {
		t.Fatalf("decode empty payload = %v; want nil", err)
	}
}

// TestDecodePayload covers parsing a payload into the destination struct.
func TestDecodePayload(t *testing.T) {
	env, err := NewEnvelope("id-2", MsgProbeReport, ProbeReportBatch{Items: []ProbeReport{{TargetHost: "h"}}})
	if err != nil {
		t.Fatal(err)
	}
	var p ProbeReportBatch
	if err := env.Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Items) != 1 || p.Items[0].TargetHost != "h" {
		t.Fatalf("decoded = %+v", p)
	}
}

// TestMarshalRoundTrip checks that the bytes from Marshal unmarshal back to the same envelope.
func TestMarshalRoundTrip(t *testing.T) {
	env, err := NewEnvelope("id-3", MsgProbeReport, ProbeReportBatch{Items: []ProbeReport{{TargetHost: "h"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Valid() || back.ID != "id-3" || back.Type != MsgProbeReport {
		t.Fatalf("round trip = %+v", back)
	}
}

// TestNormalizeKeepsKnownValues checks that valid values are kept as-is (each switch hits a case, not the default).
func TestNormalizeKeepsKnownValues(t *testing.T) {
	m := NodeMeta{
		Role:        RoleBoth,
		Direction:   DirectionIn,
		LineType:    "cmi",
		Region:      "华东",
		ISP:         "电信",
		Transport:   "ws-tls",
		BillingType: "按流量",
	}
	m.Normalize()
	if m.Role != RoleBoth || m.Direction != DirectionIn || m.LineType != "cmi" ||
		m.Transport != "ws-tls" || m.BillingType != "按流量" {
		t.Fatalf("known values must survive normalize: %+v", m)
	}
	if !strings.Contains(m.Region, "华东") {
		t.Fatalf("region = %q", m.Region)
	}
}
