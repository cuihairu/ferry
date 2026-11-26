package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
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

// wsUpgrader is the upgrader for the agent's outbound long-lived connection (P2
// explicit CheckOrigin): the authentication boundary is the hello token (sent by the agent,
// verified only after the upgrade), and the Origin header only guards against CSWSH from
// browser pages across origins -- a plain agent-to-server connection carries no Origin and is
// allowed, while one carrying an Origin must be same-origin. CheckOrigin was not set before,
// relying on gorilla's implicit default of unknown policy; the explicit form keeps the same
// behavior as that default.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // an agent-to-server connection carries no Origin
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(u.Host, r.Host)
	},
}

// maxAgentFrameBytes is the maximum size of a single WebSocket frame (P1-6): gorilla's
// default readLimit is 0 (unlimited), so a compromised or malfunctioning agent can exhaust
// panel memory with one huge frame. 8 MiB is a reasonable cap that covers oversized batch frames.
const maxAgentFrameBytes = 8 << 20

// maxBatchItems is the cap on batch items per frame (P1-6): over the cap the batch is
// truncated and an alarm is raised, so one frame cannot blow out panel memory or SQLite
// writes. 1000 items is already far beyond the normal heartbeat/periodic-report scale
// (normally single digits to tens per frame), and a single 1000-row x 14-column INSERT is still
// within the dialect's bind-variable ceiling.
const maxBatchItems = 1000

// capBatch truncates batch items to the cap; returns true when the cap was exceeded so the
// caller can raise an alarm.
func capBatch[T any](items []T, max int) ([]T, bool) {
	if len(items) <= max {
		return items, false
	}
	return items[:max], true
}

// createChunkRows is the row cap on a single batched INSERT (P1-6): an overly wide
// multi-VALUES statement can hit the dialect's bind-variable ceiling (sqlite ~32766,
// mysql/pg 65535), 1000 rows x 13 columns is far inside the safe zone, and per-chunk
// memory is released as each chunk finishes.
const createChunkRows = 1000

// createChunked writes rows in chunks: a failed chunk stops the loop and the error goes to the caller.
func createChunked[T any](db *gorm.DB, rows []T, per int) error {
	for i := 0; i < len(rows); i += per {
		end := i + per
		if end > len(rows) {
			end = len(rows)
		}
		if err := db.Create(rows[i:end]).Error; err != nil {
			return err
		}
	}
	return nil
}

// authNode looks up an enabled node by token and returns the node ID.
func (h *Handler) authNode(token string) (int64, error) {
	var revoked int64
	if err := h.db.Raw("SELECT COUNT(*) FROM node_token_revocations WHERE token = ?", token).
		Scan(&revoked).Error; err != nil {
		return 0, err
	}
	if revoked > 0 {
		return 0, errors.New("revoked token")
	}
	now := time.Now()
	var n storage.Node
	err := h.db.Where("(token = ? OR (old_token = ? AND token_rotated_at > ?)) AND enabled = ?",
		token, token, now.Add(-nodeTokenGracePeriod), true).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, errors.New("invalid token")
	}
	if err != nil {
		return 0, err
	}
	return int64(n.ID), nil
}

// nodeTokenGracePeriod is how long the previous token remains usable during
// an agent's reconnect window.
const nodeTokenGracePeriod = 10 * time.Minute

// agentWS handles the agent's outbound long-lived connection: hello auth -> register online -> heartbeat and reports.
func (h *Handler) agentWS(c *gin.Context) {
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // the Upgrade has already written the response
	}
	defer conn.Close()
	conn.SetReadLimit(maxAgentFrameBytes)

	// the first message must be hello, with authentication finished inside the deadline
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

	// registration metadata: on first registration the initial values reported by the agent are
	// recorded, and afterwards the panel is authoritative; hello_ack carries the panel's
	// authoritative values back so the agent can calibrate itself.
	meta := h.syncNodeMeta(nodeID, hello.Meta)

	// statically declared capabilities: the first agent report fills the stored
	// list (F-0228); the panel's own list wins afterwards.
	h.syncCaps(nodeID, hello.Caps)

	// the agent version is written to the DB with every hello (an A-23 self-upgrade result is a version change).
	if hello.Version != "" {
		if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).
			Update("agent_version", hello.Version).Error; err != nil {
			log.Printf("update agent_version node=%d: %v", nodeID, err)
		}
	}

	// authentication passed: reply, register, and clear the read deadline
	ack, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgHelloAck, agentproto.HelloAck{
		ServerTime:           time.Now(),
		HeartbeatIntervalSec: h.cfg.HeartbeatIntervalSec,
		Meta:                 meta,
	})
	hc := agenthub.NewConn(nodeID, conn)
	// Register before replying hello_ack: once the ack reaches the client it may trigger a panel
	// downlink request, so the connection must already be registered at that moment (otherwise the
	// request can land in the "replied but not registered" window).
	if old := h.hub.Register(hc); old != nil {
		old.Close() // the same node re-joining: replace the old connection
	}
	if err := hc.Send(ack); err != nil {
		h.hub.Unregister(nodeID, hc)
		hc.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	log.Printf("agent online node=%d agent_id=%s hostname=%s", nodeID, hello.AgentID, hello.Hostname)
	h.setNodeStatus(nodeID, "online", false)

	// OS-4 onboarding pipeline: fill in the registered IP as the address (when a provisioning
	// node has no established entry address), and asynchronously push template configuration
	// deployment for provisioning nodes (blocking on the ack, without occupying the read loop).
	if ip := c.ClientIP(); ip != "" {
		h.db.Model(&storage.Node{}).Where("id=? AND address=''", nodeID).Update("address", ip)
	}
	go h.pipelineDeploy(uint(nodeID))

	// F-0921: catch the agent up on its desired probe task set right after hello;
	// best-effort and asynchronous, the next hello retries after a failure.
	go h.dispatchProbeTasks(nodeID, hc)

	h.readAgentLoop(conn, hc, nodeID)

	h.hub.Unregister(nodeID, hc)
	hc.Close()
	h.setNodeStatus(nodeID, "offline", false)
	log.Printf("agent offline node=%d", nodeID)
}

// setNodeStatus updates the node's online status; when touch is true, last_seen is refreshed too.
// Heartbeats and online/offline transitions are high-frequency writes, so a failure is only
// logged and never breaks the connection.
func (h *Handler) setNodeStatus(nodeID int64, status string, touch bool) {
	updates := map[string]any{"status": status}
	if touch {
		updates["last_seen"] = time.Now()
	}
	// provisioning (the OS-4 onboarding pipeline) state transitions belong to the pipeline's state
	// machine: it only flips to online once the configuration deployment takes effect and the
	// target process reports running, so online/offline never overrides it; last_seen is still
	// refreshed as usual.
	res := h.db.Model(&storage.Node{}).Where("id=? AND status != 'provisioning'", nodeID).Updates(updates)
	if res.Error != nil {
		log.Printf("update node status node=%d: %v", nodeID, res.Error)
		return
	}
	if res.RowsAffected == 0 && touch {
		h.db.Model(&storage.Node{}).Where("id=?", nodeID).Update("last_seen", time.Now())
	}
}

// syncNodeMeta syncs a node's registration metadata and returns the panel's authoritative values.
// On first registration (meta_init not yet set) the initial values reported by the agent are
// recorded; afterwards the panel may change them and takes precedence, so registration no longer
// overwrites them.
func (h *Handler) syncNodeMeta(nodeID int64, reported agentproto.NodeMeta) agentproto.NodeMeta {
	var node storage.Node
	if err := h.db.Where("id=?", nodeID).First(&node).Error; err != nil {
		log.Printf("load node node=%d: %v", nodeID, err)
		return agentproto.NodeMeta{}
	}
	if node.MetaInit {
		// OS-4 metadata completion: once the panel takes over (meta_init), only the blank fields the
		// agent can probe (city/datacenter/ISP) are filled in; template and panel-set values are
		// not overwritten by registration.
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

// nodeMetaFromRow converts a node row's registration metadata back into the contract structure.
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

// saveProbeReports persists edge probe verdicts in a batch as records.
// The target node is resolved by ID or address (the panel is authoritative), and when it
// resolves, the region/ISP snapshot is filled in.
func (h *Handler) saveProbeReports(nodeID int64, items []agentproto.ProbeReport) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]storage.ProbeReport, 0, len(items))
	for _, it := range items {
		row := storage.ProbeReport{
			NodeID:         uint(nodeID),
			TargetKind:     it.TargetKind,
			TargetHost:     it.TargetHost,
			Direction:      it.Direction,
			RttMs:          it.RttMs,
			JitterMs:       it.JitterMs,
			LossPct:        it.LossPct,
			ConnectMs:      it.ConnectMs,
			TLSMs:          it.TLSMs,
			TTFBMs:         it.TTFBMs,
			ThroughputKbps: it.ThroughputKbps,
			ErrClass:       it.ErrClass,
			Reachable:      it.Reachable,
			Blocked:        it.Blocked,
			Verdict:        it.Verdict,
			Region:         it.Region,
			ISP:            it.ISP,
			ProbedAt:       it.ProbedAt,
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
	return createChunked(h.db, rows, createChunkRows)
}

// saveNodeTraffic persists node-level per-process traffic accounting in a batch (A-20).
func (h *Handler) saveNodeTraffic(nodeID int64, items []agentproto.ProcTraffic) error {
	if len(items) == 0 {
		return nil
	}
	rows := make([]storage.NodeTrafficLog, 0, len(items))
	for _, it := range items {
		rows = append(rows, storage.NodeTrafficLog{
			NodeID:       uint(nodeID),
			Proc:         it.Proc,
			RxBytes:      int64(it.Rx),
			TxBytes:      int64(it.Tx),
			DirectBytes:  int64(it.DirectBytes),
			BlockedBytes: int64(it.BlockedBytes),
			Conns:        it.Conns,
			RecordedAt:   it.At,
		})
	}
	return createChunked(h.db, rows, createChunkRows)
}

// saveUserTraffic writes per-user deltas to traffic_logs (P1-3 per-user mapping):
// the xray email is matched against users.username, and unregistered emails are logged and
// dropped rather than silently creating users.
//
// Metering scope (F-1242): user traffic is metered single-ended -- on the node
// serving the user's inbound (role entry or both, i.e. the nodes the
// subscription hands out). A node whose row cannot be resolved never bills:
// attribution needs a role verdict (F-1243 then extends this to landing).
func (h *Handler) saveUserTraffic(nodeID int64, items []agentproto.ProcTraffic) error {
	var nrow storage.Node
	if err := h.db.Select("id, role").First(&nrow, nodeID).Error; err != nil {
		log.Printf("user traffic node=%d: node row unresolved, report dropped: %v", nodeID, err)
		return nil
	}
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
	// Emails that miss the current username are still attributed through rename aliases (P2 fix
	// for under-counted traffic): after a rename, the old email keeps being reported while the
	// node-side config has not been re-pushed, and the old name is retained as an alias -- so
	// the alias is a fallback mapping back to the original user, not a silent drop. Matching
	// the current username takes precedence (added to the map first).
	aliasEmails := make([]string, 0)
	for e := range emails {
		if _, ok := byName[e]; !ok {
			aliasEmails = append(aliasEmails, e)
		}
	}
	if len(aliasEmails) > 0 {
		var aliases []storage.UserAlias
		if err := h.db.Select("user_id, username").Where("username IN ?", aliasEmails).Find(&aliases).Error; err != nil {
			return err
		}
		for _, a := range aliases {
			if _, ok := byName[a.Username]; !ok {
				byName[a.Username] = a.UserID
			}
		}
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
			// UserID may be nil (P2: deleting a user sets it to NULL to preserve history); a live user always has one.
			rows = append(rows, storage.TrafficLog{
				UserID: &uid, NodeID: &nid,
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
	if err := createChunked(h.db, rows, createChunkRows); err != nil {
		return err
	}
	// G2 user online state and most recent node: any traffic delta this round counts as active, so
	// last_node_id / last_seen_at are refreshed in a batch over the known user set (the
	// node-level row is written separately by node_id).
	active := make([]uint, 0, len(byName))
	for _, uid := range byName {
		active = append(active, uid)
	}
	now := time.Now()
	return h.db.Model(&storage.User{}).Where("id IN ?", active).
		Updates(map[string]any{"last_node_id": nid, "last_seen_at": now}).Error
}

// findNodeByAddress resolves a node by host:port.
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
			// Runtime capability state rides each heartbeat (F-0229); an empty
			// payload (old agent) is a no-op.
			h.mergeCaps(nodeID, hb.Caps)
			// Structured relay capability declaration (F-0804): an empty payload
			// from an older agent must not erase a previously reported declaration.
			if hb.RelayCaps != nil {
				if b, err := json.Marshal(hb.RelayCaps); err == nil {
					if err := h.db.Model(&storage.Node{}).Where("id=?", nodeID).
						Update("relay_caps", string(b)).Error; err != nil {
						log.Printf("save relay caps node=%d: %v", nodeID, err)
					}
				}
			}
			// Persist the process snapshot (SAVE-3): metrics (cache hit statistics, etc.) are updated
			// with it, and a failure only logs -- it never blocks the heartbeat reply.
			if procs, over := capBatch(hb.Procs, maxBatchItems); over {
				log.Printf("heartbeat proc batch over limit node=%d: %d", nodeID, len(hb.Procs))
				hb.Procs = procs
			}
			if err := h.saveProcStatuses(nodeID, hb.Procs); err != nil {
				log.Printf("save proc statuses node=%d: %v", nodeID, err)
			}
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
			// Report idempotency (FERRY-004, semantics in `docs/design/agent架构设计.md` §8):
			// a duplicate frame skips the side effects but still gets its ack, so the agent
			// can drop its retransmission on seeing the ack.
			if !h.repeatReport(nodeID, env.Type, env.ID) {
				h.recordAlert(uint(nodeID), al)
			}
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgAlarmAck, nil)
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgProbeReport:
			var pr agentproto.ProbeReportBatch
			if err := env.Decode(&pr); err != nil {
				continue
			}
			if items, over := capBatch(pr.Items, maxBatchItems); over {
				log.Printf("probe batch over limit node=%d: %d", nodeID, len(pr.Items))
				pr.Items = items
			}
			if !h.repeatReport(nodeID, env.Type, env.ID) {
				if err := h.saveProbeReports(nodeID, pr.Items); err != nil {
					log.Printf("save probe reports node=%d: %v", nodeID, err)
				}
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
			if procs, over := capBatch(pr.Procs, maxBatchItems); over {
				log.Printf("proc report batch over limit node=%d: %d", nodeID, len(pr.Procs))
				pr.Procs = procs
			}
			// One-way message with no ack: dedup only guards against repeated side effects (FERRY-004).
			if h.repeatReport(nodeID, env.Type, env.ID) {
				continue
			}
			// A process back to running -> the matching crash alert resolves itself (A-22);
			// for a provisioning node, the target process reporting running is the "probe passed"
			// verdict (OS-4).
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
			if items, over := capBatch(tr.Items, maxBatchItems); over {
				log.Printf("traffic batch over limit node=%d: %d", nodeID, len(tr.Items))
				tr.Items = items
			}
			// traffic carries periodic deltas (a billing figure), so a duplicated frame counted twice means
			// overcharging; idempotency must be enforced before accounting, and the ack is unaffected
			// (FERRY-004).
			if !h.repeatReport(nodeID, env.Type, env.ID) {
				if err := h.saveNodeTraffic(nodeID, tr.Items); err != nil {
					log.Printf("save node traffic node=%d: %v", nodeID, err)
				}
				if err := h.saveUserTraffic(nodeID, tr.Items); err != nil {
					log.Printf("save user traffic node=%d: %v", nodeID, err)
				}
			}
			reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgTrafficAck, agentproto.TrafficAck{
				Recorded: len(tr.Items),
			})
			if err := hc.Send(reply); err != nil {
				return
			}
		case agentproto.MsgUpgradeStatus:
			// Self-upgrade progress (F-0712): one-way report keyed by the command id; the envelope
			// id is "<command>-<state>", so dedupe absorbs retransmissions of the same state.
			var us agentproto.UpgradeStatus
			if err := env.Decode(&us); err != nil || !agentproto.ValidUpgradeState(us.State) {
				continue
			}
			if h.repeatReport(nodeID, env.Type, env.ID) {
				continue
			}
			h.recordUpgradeStatus(nodeID, us)
		case agentproto.MsgCalibrate:
			reply, ok := h.handleCalibrate(nodeID, env)
			if !ok {
				continue
			}
			if err := hc.Send(reply); err != nil {
				return
			}
		default:
			// Reply-style messages go to whichever request is waiting (P2 node-ownership check: only this
			// node's pending waiters count; a bogus cross-node reply with the same ID finds no
			// waiter and is dropped).
			if h.hub.Deliver(nodeID, env) {
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
