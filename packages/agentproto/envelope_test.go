package agentproto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// roundTrip 序列化后还原，断言字段一致。
func roundTrip(t *testing.T, e Envelope, want any) {
	t.Helper()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !got.Valid() {
		t.Fatal("envelope version invalid after round trip")
	}
	var decoded map[string]any
	if err := json.Unmarshal(got.Payload, &decoded); err != nil {
		t.Fatalf("payload not valid json: %v", err)
	}
	wantRaw, _ := json.Marshal(want)
	if !bytes.Equal(got.Payload, wantRaw) {
		t.Fatalf("payload mismatch:\n got %s\nwant %s", got.Payload, wantRaw)
	}
}

func TestNewEnvelopeRoundTrip(t *testing.T) {
	h := Hello{Token: "tok", AgentID: "node-1", Version: "dev", StartedAt: time.Unix(0, 0).UTC()}
	e, err := NewEnvelope("id-1", MsgHello, h)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if e.Type != MsgHello || e.ID != "id-1" {
		t.Fatalf("unexpected envelope header: %+v", e)
	}
	roundTrip(t, e, h)
}

func TestEnvelopeDecode(t *testing.T) {
	push := ConfigPush{Proc: "xray", Kind: "xray", Version: "v2", Sha256: "ab", Payload: "{}"}
	e, err := NewEnvelope("", MsgConfigPush, push)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	var got ConfigPush
	if err := e.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != push {
		t.Fatalf("decode mismatch: %+v", got)
	}
}

func TestEnvelopeEmptyPayload(t *testing.T) {
	e, err := NewEnvelope("id", MsgHelloAck, nil)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	if len(e.Payload) != 0 {
		t.Fatalf("expected empty payload, got %s", e.Payload)
	}
	var ack HelloAck
	if err := e.Decode(&ack); err != nil {
		t.Fatalf("decode empty payload should not error: %v", err)
	}
}

func TestEnvelopeVersionGuard(t *testing.T) {
	e := Envelope{V: ProtocolVersion + 1, Type: MsgHello}
	if e.Valid() {
		t.Fatal("future version must be rejected")
	}
}

// TestMessageTypesStable 锁定消息类型字符串，防意外改动破坏两端兼容。
func TestMessageTypesStable(t *testing.T) {
	want := map[string]string{
		MsgHello:        "agent.hello",
		MsgHelloAck:     "panel.hello_ack",
		MsgHeartbeat:    "agent.heartbeat",
		MsgHeartbeatAck: "panel.heartbeat_ack",
		MsgAlarm:        "agent.alarm",
		MsgAlarmAck:     "panel.alarm_ack",
		MsgTraffic:      "agent.traffic",
		MsgTrafficAck:   "panel.traffic_ack",
		MsgProcReport:   "agent.proc_report",
		MsgProcCtl:      "panel.proc_ctl",
		MsgProcCtlAck:   "agent.proc_ctl_ack",
		MsgConfigPush:   "panel.config_push",
		MsgConfigAck:    "agent.config_ack",
	}
	if len(want) != 13 {
		t.Fatalf("message type table out of sync: %d entries", len(want))
	}
}

func TestHeartbeatAndTrafficRoundTrip(t *testing.T) {
	hb := Heartbeat{
		UptimeSec:     120,
		Load1:         0.42,
		MemUsedBytes:  1 << 20,
		MemTotalBytes: 1 << 30,
		Certs:         []CertStatus{{Domain: "a.example.com", NotAfter: time.Unix(100, 0).UTC()}},
		Procs:         []ProcStatus{{Name: "xray", State: ProcRunning, Since: time.Unix(50, 0).UTC(), Restarts: 1, PID: 7}},
		At:            time.Unix(200, 0).UTC(),
	}
	e, err := NewEnvelope("hb-1", MsgHeartbeat, hb)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	roundTrip(t, e, hb)

	tr := TrafficReport{Items: []ProcTraffic{{Proc: "xray", Rx: 10, Tx: 20, Conns: 3, At: time.Unix(1, 0).UTC()}}}
	e2, err := NewEnvelope("tr-1", MsgTraffic, tr)
	if err != nil {
		t.Fatalf("new envelope: %v", err)
	}
	roundTrip(t, e2, tr)
}

func TestNodeMetaNormalize(t *testing.T) {
	// 零值应补齐缺省，region/isp 不许空。
	m := NodeMeta{}
	m.Normalize()
	if m.Role != RoleLanding || m.Direction != DirectionOut {
		t.Fatalf("role/direction defaults wrong: %+v", m)
	}
	if m.LineType != "普通" || m.Region != "未知" || m.ISP != "未知" {
		t.Fatalf("line/region/isp defaults wrong: %+v", m)
	}
	if m.Transport != "tls" || m.BillingType != "包月" || m.Currency != "CNY" {
		t.Fatalf("transport/billing/currency defaults wrong: %+v", m)
	}
	// 非法取值收敛到默认，合法取值原样保留。
	m = NodeMeta{Role: "gateway", Direction: "x", LineType: "y", Transport: "raw",
		BillingType: "z", Region: "华东", ISP: "电信", Labels: []string{"bgp"}}
	m.Normalize()
	if m.Role != RoleLanding || m.Direction != DirectionOut || m.LineType != "普通" ||
		m.Transport != "tls" || m.BillingType != "包月" {
		t.Fatalf("invalid values must clamp: %+v", m)
	}
	if m.Region != "华东" || m.ISP != "电信" || len(m.Labels) != 1 {
		t.Fatalf("valid values must survive: %+v", m)
	}
}
