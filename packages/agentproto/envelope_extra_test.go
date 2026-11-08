package agentproto

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNewEnvelopeMarshalError 载荷不可序列化（函数值）：组包报错。
func TestNewEnvelopeMarshalError(t *testing.T) {
	if _, err := NewEnvelope("id-1", MsgProbeReport, func() {}); err == nil {
		t.Fatal("unmarshalable payload must error")
	}
}

// TestDecodeEmptyPayload 空载荷 Decode 直接成功且不动目标。
func TestDecodeEmptyPayload(t *testing.T) {
	var p ProbeReportBatch
	if err := (Envelope{V: ProtocolVersion, Type: MsgProbeReport}).Decode(&p); err != nil {
		t.Fatalf("decode empty payload = %v; want nil", err)
	}
}

// TestDecodePayload 载荷能解析进目标结构。
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

// TestMarshalRoundTrip Marshal 编出的报文能解回同值信封。
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

// TestNormalizeKeepsKnownValues 合法取值原样保留（各 switch 命中 case 而非 default）。
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
