package handler

import (
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

var wsUpgrader = websocket.Upgrader{}

// authNode 按令牌查启用的节点，返回节点 ID。
func (h *Handler) authNode(token string) (int64, error) {
	var n storage.Node
	err := h.db.Where("token=? AND enabled=?", token, true).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, errors.New("invalid token")
	}
	if err != nil {
		return 0, err
	}
	return int64(n.ID), nil
}

// agentWS 处理 agent 的出站长连接：hello 认证 → 注册在线 → 心跳与上报。
func (h *Handler) agentWS(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade 已写回响应
	}
	defer conn.Close()

	// 第一条消息必须是 hello，限时完成认证。
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	env, err := readEnvelope(conn)
	if err != nil || env.Type != agentproto.MsgHello {
		closeWS(conn, websocket.CloseProtocolError, "expect hello")
		return
	}
	var hello agentproto.Hello
	if err := env.Decode(&hello); err != nil || hello.Token == "" {
		closeWS(conn, websocket.CloseProtocolError, "bad hello")
		return
	}
	nodeID, err := h.authNode(hello.Token)
	if err != nil {
		log.Printf("agent auth failed: %v", err)
		closeWS(conn, websocket.ClosePolicyViolation, "invalid token")
		return
	}

	// 认证通过：应答、注册、解除读限时。
	ack, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgHelloAck, agentproto.HelloAck{
		ServerTime:           time.Now(),
		HeartbeatIntervalSec: h.cfg.HeartbeatIntervalSec,
	})
	hc := agenthub.NewConn(nodeID, conn)
	if err := hc.Send(ack); err != nil {
		return
	}
	if old := h.hub.Register(hc); old != nil {
		old.Close() // 同节点重复接入，顶替旧连接
	}
	_ = conn.SetReadDeadline(time.Time{})
	log.Printf("agent online node=%d agent_id=%s hostname=%s", nodeID, hello.AgentID, hello.Hostname)
	h.setNodeStatus(nodeID, "online", false)

	h.readAgentLoop(conn, hc, nodeID)

	h.hub.Unregister(nodeID, hc)
	hc.Close()
	h.setNodeStatus(nodeID, "offline", false)
	log.Printf("agent offline node=%d", nodeID)
}

// setNodeStatus 更新节点在线状态；touch 为真时同时刷新 last_seen。
// 心跳与上下线是高频写，失败仅记日志不打断连接。
func (h *Handler) setNodeStatus(nodeID int64, status string, touch bool) {
	updates := map[string]any{"status": status}
	if touch {
		updates["last_seen"] = time.Now()
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).Updates(updates).Error; err != nil {
		log.Printf("update node status node=%d: %v", nodeID, err)
	}
}

func (h *Handler) readAgentLoop(conn *websocket.Conn, hc *agenthub.Conn, nodeID int64) {
	for {
		env, err := readEnvelope(conn)
		if err != nil {
			return
		}
		switch env.Type {
		case agentproto.MsgHeartbeat:
			var hb agentproto.Heartbeat
			if err := env.Decode(&hb); err != nil {
				continue
			}
			h.setNodeStatus(nodeID, "online", true)
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgHeartbeatAck, agentproto.HeartbeatAck{
				NextIntervalSec: h.cfg.HeartbeatIntervalSec,
			})
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgAlarm:
			var al agentproto.Alarm
			if err := env.Decode(&al); err != nil {
				continue
			}
			// 告警落库与展示在管理面批次接入，先记录保证不丢日志。
			log.Printf("alarm node=%d kind=%s severity=%s proc=%s: %s", nodeID, al.Kind, al.Severity, al.Proc, al.Message)
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgAlarmAck, nil)
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgProcReport:
			var pr agentproto.ProcReport
			if err := env.Decode(&pr); err != nil {
				continue
			}
			log.Printf("proc report node=%d: %d procs", nodeID, len(pr.Procs))
		default:
			// 应答类消息交给等待中的请求。
			if h.hub.Deliver(env) {
				continue
			}
			log.Printf("unhandled message node=%d type=%s", nodeID, env.Type)
		}
	}
}

func readEnvelope(conn *websocket.Conn) (agentproto.Envelope, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return agentproto.Envelope{}, err
	}
	var env agentproto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return agentproto.Envelope{}, err
	}
	if !env.Valid() {
		return agentproto.Envelope{}, errors.New("unsupported protocol version")
	}
	return env, nil
}

func closeWS(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason), time.Now().Add(3*time.Second))
	_ = conn.Close()
}
