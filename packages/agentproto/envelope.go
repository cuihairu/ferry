// Package agentproto defines the message contract between the ferry panel and ferry-agent.
// Both sides share this package; the transport is WebSocket and the encoding is JSON throughout.
// The agent only opens outbound connections to the panel.
package agentproto

import "encoding/json"

// ProtocolVersion is the message contract version; bump it on breaking changes.
const ProtocolVersion = 1

// Message types: agent→panel use the agent. prefix, panel→agent use the panel. prefix.
const (
	MsgHello         = "agent.hello"          // handshake, carries the node token
	MsgHelloAck      = "panel.hello_ack"      // handshake reply
	MsgHeartbeat     = "agent.heartbeat"      // periodic heartbeat
	MsgHeartbeatAck  = "panel.heartbeat_ack"  // heartbeat reply
	MsgAlarm         = "agent.alarm"          // anomaly alarm
	MsgAlarmAck      = "panel.alarm_ack"      // alarm reply
	MsgTraffic       = "agent.traffic"        // traffic and online-count report
	MsgTrafficAck    = "panel.traffic_ack"    // traffic report reply
	MsgProcReport    = "agent.proc_report"    // process state event report
	MsgProcCtl       = "panel.proc_ctl"       // panel process control command
	MsgProcCtlAck    = "agent.proc_ctl_ack"   // process control reply
	MsgProcLogs      = "panel.proc_logs"      // panel process log fetch request (P1-11)
	MsgProcLogsAck   = "agent.proc_logs_ack"  // process log fetch reply
	MsgConfigPush    = "panel.config_push"    // config push
	MsgConfigAck     = "agent.config_ack"     // config push reply
	MsgProbeReport   = "agent.probe_report"   // edge probe verdict report
	MsgProbeAck      = "panel.probe_ack"      // probe report reply
	MsgProbeTask     = "panel.probe_task"     // probe task dispatch (F-0921)
	MsgProbeTaskAck  = "agent.probe_task_ack" // probe task dispatch reply (F-0921)
	MsgCalibrate     = "agent.calibrate"      // speed calibration report (E-8)
	MsgCalibrateAck  = "panel.calibrate_ack"  // speed calibration reply
	MsgUpgrade       = "panel.upgrade"        // self-upgrade command (A-23)
	MsgUpgradeAck    = "agent.upgrade_ack"    // self-upgrade reply
	MsgUpgradeStatus = "agent.upgrade_status" // self-upgrade progress report (F-0712)
)

// Envelope is the common wrapper for all messages. The requester sets ID; the replier echoes it back.
type Envelope struct {
	V       int             `json:"v"` // protocol version, must equal ProtocolVersion
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope assembles a message; a nil payload means no payload.
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

// Decode unmarshals the payload into v. An empty payload returns nil.
func (e Envelope) Decode(v any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(e.Payload, v)
}

// Marshal encodes the message for transport.
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// Valid checks the message version; unknown versions are rejected outright.
func (e Envelope) Valid() bool { return e.V == ProtocolVersion }
