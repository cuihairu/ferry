package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
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

	// agent 版本随每次 hello 落库（A-23 自升级结果即版本变化）。
	if hello.Version != "" {
		if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).
			Update("agent_version", hello.Version).Error; err != nil {
			log.Printf("update agent_version node=%d: %v", nodeID, err)
		}
	}

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

	// OS-4 入池流水线：注册 IP 补地址（供给节点无既定入口地址时），
	// provisioning 节点异步推进模板配置下发（阻塞等 ack，不占连接读循环）。
	if ip := c.ClientIP(); ip != "" {
		h.db.Model(&storage.Node{}).Where("id=? AND address=''", nodeID).Update("address", ip)
	}
	go h.pipelineDeploy(uint(nodeID))

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
	// provisioning（OS-4 供给流水线）的状态迁移归流水线状态机：配置下发生效
	// 且目标进程 running 上报才转 online，上下线不覆盖；last_seen 照常刷新。
	res := h.db.Model(&storage.Node{}).Where("id=? AND status != 'provisioning'", nodeID).Updates(updates)
	if res.Error != nil {
		log.Printf("update node status node=%d: %v", nodeID, res.Error)
		return
	}
	if res.RowsAffected == 0 && touch {
		h.db.Model(&storage.Node{}).Where("id=?", nodeID).Update("last_seen", time.Now())
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
		// OS-4 元数据补全：面板接管（meta_init）下只补 agent 可探测的空白
		// 字段（城市/机房/运营商），模板与面板既定值不被注册覆盖。
		fills := map[string]any{}
		if node.City == "" && reported.City != "" {
			fills["city"] = reported.City
		}
		if node.Datacenter == "" && reported.Datacenter != "" {
			fills["datacenter"] = reported.Datacenter
		}
		if node.ISP == "" && reported.ISP != "" {
			fills["isp"] = reported.ISP
		}
		if len(fills) > 0 {
			if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).Updates(fills).Error; err != nil {
				log.Printf("fill node meta node=%d: %v", nodeID, err)
				return nodeMetaFromRow(&node)
			}
			var updated storage.Node
			if err := h.db.Where("id=?", nodeID).First(&updated).Error; err == nil {
				node = updated
			}
		}
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

// saveUserTraffic 把 per-user 增量落 traffic_logs（P1-3 逐用户映射）：
// xray email 按 users.username 匹配，未注册的邮箱记日志丢弃，不静默造用户。
func (h *Handler) saveUserTraffic(nodeID int64, items []agentproto.ProcTraffic) error {
	emails := map[string]bool{}
	for _, it := range items {
		for _, u := range it.Users {
			if u.Email != "" {
				emails[u.Email] = true
			}
		}
	}
	if len(emails) == 0 {
		return nil
	}
	names := make([]string, 0, len(emails))
	for e := range emails {
		names = append(names, e)
	}
	var users []storage.User
	if err := h.db.Select("id, username").Where("username IN ?", names).Find(&users).Error; err != nil {
		return err
	}
	byName := make(map[string]uint, len(users))
	for _, u := range users {
		byName[u.Username] = u.ID
	}

	nid := uint(nodeID)
	rows := make([]storage.TrafficLog, 0, len(names))
	unknown := map[string]bool{}
	for _, it := range items {
		for _, u := range it.Users {
			uid, ok := byName[u.Email]
			if !ok {
				unknown[u.Email] = true
				continue
			}
			rows = append(rows, storage.TrafficLog{
				UserID: uid, NodeID: &nid,
				RxBytes: int64(u.Rx), TxBytes: int64(u.Tx), RecordedAt: it.At,
			})
		}
	}
	if len(unknown) > 0 {
		missed := make([]string, 0, len(unknown))
		for e := range unknown {
			missed = append(missed, e)
		}
		sort.Strings(missed)
		log.Printf("user traffic node=%d: unknown emails dropped: %s", nodeID, strings.Join(missed, ","))
	}
	if len(rows) == 0 {
		return nil
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
			h.recordAlert(uint(nodeID), al)
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
			// 进程恢复运行 → 自动消解对应的崩溃告警（A-22）；
			// provisioning 节点的目标进程 running 即「探测通过」判定（OS-4）。
			for _, ps := range pr.Procs {
				if ps.State == "running" {
					h.resolveProcCrashAlerts(uint(nodeID), ps.Name)
					h.pipelineProbePass(uint(nodeID), ps.Name)
				}
			}
		case agentproto.MsgTraffic:
			var tr agentproto.TrafficReport
			if err := env.Decode(&tr); err != nil {
				continue
			}
			if err := h.saveNodeTraffic(nodeID, tr.Items); err != nil {
				log.Printf("save node traffic node=%d: %v", nodeID, err)
			}
			if err := h.saveUserTraffic(nodeID, tr.Items); err != nil {
				log.Printf("save user traffic node=%d: %v", nodeID, err)
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
