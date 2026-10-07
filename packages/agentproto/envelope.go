// Package agentproto 定义 ferry 面板与 ferry-agent 之间的消息契约。
// 双方共用本包；传输层为 WebSocket，编码统一 JSON。agent 只出站连接面板。
package agentproto

import "encoding/json"

// ProtocolVersion 是消息契约版本，破坏性变更时递增。
const ProtocolVersion = 1

// 消息类型：agent→panel 用 agent. 前缀，panel→agent 用 panel. 前缀。
const (
	MsgHello        = "agent.hello"         // 握手，携带节点令牌
	MsgHelloAck     = "panel.hello_ack"     // 握手应答
	MsgHeartbeat    = "agent.heartbeat"     // 周期心跳
	MsgHeartbeatAck = "panel.heartbeat_ack" // 心跳应答
	MsgAlarm        = "agent.alarm"         // 异常告警
	MsgAlarmAck     = "panel.alarm_ack"     // 告警应答
	MsgTraffic      = "agent.traffic"       // 流量与在线数上报
	MsgTrafficAck   = "panel.traffic_ack"   // 流量上报应答
	MsgProcReport   = "agent.proc_report"   // 进程状态事件上报
	MsgProcCtl      = "panel.proc_ctl"      // 面板进程操作指令
	MsgProcCtlAck   = "agent.proc_ctl_ack"  // 进程操作应答
	MsgConfigPush   = "panel.config_push"   // 配置下发
	MsgConfigAck    = "agent.config_ack"    // 配置下发应答
	MsgProbeReport  = "agent.probe_report"  // 边缘探测结论上报
	MsgProbeAck     = "panel.probe_ack"     // 探测上报应答
	MsgCalibrate    = "agent.calibrate"     // 测速校准上报（E-8）
	MsgCalibrateAck = "panel.calibrate_ack" // 测速校准应答
)

// Envelope 是所有消息的统一封装。请求方设置 ID，应答方原样带回。
type Envelope struct {
	V       int             `json:"v"` // 协议版本，须等于 ProtocolVersion
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope 组装一条消息，payload 为 nil 时表示无载荷。
func NewEnvelope(id, msgType string, payload any) (Envelope, error) {
	e := Envelope{V: ProtocolVersion, ID: id, Type: msgType}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		e.Payload = raw
	}
	return e, nil
}

// Decode 将载荷解析到 v。空载荷直接返回 nil。
func (e Envelope) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// Marshal 编码为传输报文。
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// Valid 校验消息版本，未知版本直接拒绝。
func (e Envelope) Valid() bool { return e.V == ProtocolVersion }
