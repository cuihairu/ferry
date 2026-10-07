package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"strconv"
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

	// 注册元数据：首次注册落 agent 上报的初值，之后以面板为准；
	// hello_ack 回传面板权威值供 agent 校准。
	meta := h.syncNodeMeta(nodeID, hello.Meta)

	// 认证通过：应答、注册、解除读限时。
	ack, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgHelloAck, agentproto.HelloAck{
		ServerTime:           time.Now(),
		HeartbeatIntervalSec: h.cfg.HeartbeatIntervalSec,
		Meta:                 meta,
	})
	hc := agenthub.NewConn(nodeID, conn)
	// 先注册再回 hello_ack：ack 到达客户端即可能触发面板下发请求，
	// 必须保证此刻连接已在册（否则请求会撞上「已应答但未注册」的窗口）。
	if old := h.hub.Register(hc); old != nil {
		old.Close() // 同节点重复接入，顶替旧连接
	}
	if err := hc.Send(ack); err != nil {
		h.hub.Unregister(nodeID, hc)
		hc.Close()
		return
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

// syncNodeMeta 同步节点注册元数据，返回面板权威值。
// 首次注册（meta_init 未置位）落 agent 上报的初值；
// 之后面板可改且以面板为准，注册不再覆盖。
func (h *Handler) syncNodeMeta(nodeID int64, reported agentproto.NodeMeta) agentproto.NodeMeta {
	var node storage.Node
	if err := h.db.Where("id=?", nodeID).First(&node).Error; err != nil {
		log.Printf("load node node=%d: %v", nodeID, err)
		return agentproto.NodeMeta{}
	}
	if node.MetaInit {
		return nodeMetaFromRow(&node)
	}
	m := reported
	m.Normalize()
	labels, _ := json.Marshal(m.Labels)
	updates := map[string]any{
		"role": m.Role, "direction": m.Direction, "line_type": m.LineType,
		"region": m.Region, "city": m.City, "datacenter": m.Datacenter,
		"isp": m.ISP, "labels": string(labels), "transport": m.Transport,
		"billing_type":                m.BillingType,
		"traffic_price_cents":         m.TrafficPriceCents,
		"monthly_cost_cents":          m.MonthlyCostCents,
		"currency":                    m.Currency,
		"cost_note":                   m.CostNote,
		"bw_up_mbps":                  m.BwUpMbps,
		"bw_down_mbps":                m.BwDownMbps,
		"monthly_traffic_quota_bytes": m.MonthlyTrafficQuota,
		"rate_limited":                m.RateLimited,
		"burst":                       m.Burst,
		"meta_init":                   true,
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).Updates(updates).Error; err != nil {
		log.Printf("apply node meta node=%d: %v", nodeID, err)
		return agentproto.NodeMeta{}
	}
	return m
}

// nodeMetaFromRow 把节点行的注册元数据转回契约结构。
func nodeMetaFromRow(n *storage.Node) agentproto.NodeMeta {
	var labels []string
	if n.Labels != "" {
		_ = json.Unmarshal([]byte(n.Labels), &labels)
	}
	return agentproto.NodeMeta{
		Role: n.Role, Direction: n.Direction, LineType: n.LineType,
		Region: n.Region, City: n.City, Datacenter: n.Datacenter,
		ISP: n.ISP, Labels: labels, Transport: n.Transport,
		BillingType:       n.BillingType,
		TrafficPriceCents: n.TrafficPriceCents, MonthlyCostCents: n.MonthlyCostCents,
		Currency: n.Currency, CostNote: n.CostNote,
		BwUpMbps: n.BwUpMbps, BwDownMbps: n.BwDownMbps,
		MonthlyTrafficQuota: n.MonthlyTrafficQuotaBytes,
		RateLimited:         n.RateLimited, Burst: n.Burst,
	}
}

// saveProbeReports 批量落边缘探测结论存证。
// 目标节点按 ID 或地址解析（面板为权威），解析到则补全区域/运营商快照。
func (h *Handler) saveProbeReports(nodeID int64, items []agentproto.ProbeReport) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]storage.ProbeReport, 0, len(items))
	for _, it := range items {
		row := storage.ProbeReport{
			NodeID:     uint(nodeID),
			TargetKind: it.TargetKind,
			TargetHost: it.TargetHost,
			Direction:  it.Direction,
			RttMs:      it.RttMs,
			LossPct:    it.LossPct,
			Reachable:  it.Reachable,
			Blocked:    it.Blocked,
			Verdict:    it.Verdict,
			Region:     it.Region,
			ISP:        it.ISP,
			ProbedAt:   it.ProbedAt,
		}
		switch {
		case it.TargetNode > 0:
			var n storage.Node
			if err := h.db.Where("id=?", it.TargetNode).First(&n).Error; err == nil {
				row.TargetNodeID = &n.ID
				row.Region = n.Region
				row.ISP = n.ISP
			} else {
				id := uint(it.TargetNode)
				row.TargetNodeID = &id
			}
		default:
			if n := h.findNodeByAddress(it.TargetHost); n != nil {
				row.TargetNodeID = &n.ID
				row.Region = n.Region
				row.ISP = n.ISP
			}
		}
		rows = append(rows, row)
	}
	return h.db.Create(&rows).Error
}

// saveNodeTraffic 批量落节点级进程流量记账（A-20）。
func (h *Handler) saveNodeTraffic(nodeID int64, items []agentproto.ProcTraffic) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]storage.NodeTrafficLog, 0, len(items))
	for _, it := range items {
		rows = append(rows, storage.NodeTrafficLog{
			NodeID:     uint(nodeID),
			Proc:       it.Proc,
			RxBytes:    int64(it.Rx),
			TxBytes:    int64(it.Tx),
			Conns:      it.Conns,
			RecordedAt: it.At,
		})
	}
	return h.db.Create(&rows).Error
}

// findNodeByAddress 按 host:port 解析节点。
func (h *Handler) findNodeByAddress(hostport string) *storage.Node {
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}
	var n storage.Node
	if err := h.db.Where("address=? AND port=?", host, port).First(&n).Error; err != nil {
		return nil
	}
	return &n
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
		case agentproto.MsgProbeReport:
			var pr agentproto.ProbeReportBatch
			if err := env.Decode(&pr); err != nil {
				continue
			}
			if err := h.saveProbeReports(nodeID, pr.Items); err != nil {
				log.Printf("save probe reports node=%d: %v", nodeID, err)
			}
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProbeAck, agentproto.ProbeAck{
				Recorded: len(pr.Items),
			})
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgProcReport:
			var pr agentproto.ProcReport
			if err := env.Decode(&pr); err != nil {
				continue
			}
			log.Printf("proc report node=%d: %d procs", nodeID, len(pr.Procs))
		case agentproto.MsgTraffic:
			var tr agentproto.TrafficReport
			if err := env.Decode(&tr); err != nil {
				continue
			}
			if err := h.saveNodeTraffic(nodeID, tr.Items); err != nil {
				log.Printf("save node traffic node=%d: %v", nodeID, err)
			}
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgTrafficAck, agentproto.TrafficAck{
				Recorded: len(tr.Items),
			})
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgCalibrate:
			reply, ok := h.handleCalibrate(nodeID, env)
			if !ok {
				continue
			}
			if err := hc.Send(reply); err != nil {
				return
			}
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
