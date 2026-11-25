// Package xraystats talks to the xray gRPC stats API to collect real per-user traffic (P1-3,
// source: Marzban's xray_api/stats.py QueryStats).
// The client only reads QueryStats: pattern=user>>> with reset=true, yielding the per-email up/down
// deltas since the last query; it connects straight to the node's localhost (the api is exposed on 127.0.0.1 only).
// The transport layer uses this package's minimal unary client from grpc.go (per the E-4 core size budget,
// without importing google.golang.org/grpc).
package xraystats

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// queryStatsMethod is the full name of StatsService's QueryStats (the official xray proto).
const queryStatsMethod = "/xray.app.stats.command.StatsService/QueryStats"

// collectTimeout is the timeout of one stats query.
const collectTimeout = 5 * time.Second

// Stat is one counter of a QueryStats result.
type Stat struct {
	Name  string
	Value uint64
}

// UserBytes is one user's up/down byte delta.
type UserBytes struct {
	Rx uint64 // bytes received by the user (downlink)
	Tx uint64 // bytes sent by the user (uplink)
}

// Client is the xray stats client: it builds a fresh connection per query (the collection period is in seconds,
// so there is no reconnect state machine and it heals itself after an xray restart).
type Client struct {
	addr string // the xray api listener, e.g. 127.0.0.1:10085
	log  *log.Logger
}

// New creates a client.
func New(addr string, logger *log.Logger) *Client {
	if logger == nil {
		logger = log.Default()
	}
	return &Client{addr: addr, log: logger}
}

// QueryUserStats queries and resets all user statistics, returning the per-email up/down deltas.
// The xray side must enable per-user statistics (policy.levels[].statsUserUplink/Downlink
// plus the stats service/api inbound -- a deployment-side rule); when it is off, an empty table returns without error.
func (c *Client) QueryUserStats(ctx context.Context) (map[string]UserBytes, error) {
	payload, err := grpcUnary(ctx, c.addr, queryStatsMethod, encodeQueryStatsReq("user>>>", true))
	if err != nil {
		return nil, fmt.Errorf("QueryStats %s: %w", c.addr, err)
	}
	stats, err := decodeQueryStatsResp(payload)
	if err != nil {
		return nil, fmt.Errorf("decode QueryStats: %w", err)
	}
	return ParseUserStats(stats), nil
}

// ParseUserStats aggregates a QueryStats result into per-email up/down:
// a name looks like user>>>EMAIL>>>traffic>>>uplink|downlink, and anything else is ignored.
// Semantics align with traffic_logs' user view: downlink = received (rx), uplink = sent (tx).
func ParseUserStats(stats []Stat) map[string]UserBytes {
	out := map[string]UserBytes{}
	for _, s := range stats {
		parts := strings.SplitN(s.Name, ">>>", 4)
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		var u UserBytes
		switch parts[3] {
		case "downlink":
			u = out[parts[1]]
			u.Rx += s.Value
		case "uplink":
			u = out[parts[1]]
			u.Tx += s.Value
		default:
			continue
		}
		out[parts[1]] = u
	}
	return out
}

// the routed outbound tags (SAVE-1 direct / SAVE-4 blocked, matching the outbounds routing.Merge adds).
const (
	directOutboundTag = "direct"
	blockOutboundTag  = "block"
)

// OutboundBytes is the routed outbound byte delta within one query cycle (a savings-report data source).
type OutboundBytes struct {
	Direct  uint64 // direct routed outbound (SAVE-1: hits on domestic/static lists never enter the tunnel)
	Blocked uint64 // blocked outbound (SAVE-4: the request bytes the blackhole received)
}

// QueryOutboundStats queries and resets the routed outbound traffic counters, returning this cycle's
// direct and blocked byte deltas. reset=true zeroes every outbound counter, so the direct and
// blocked ones must be taken in the same query (querying them separately would zero each other out).
// when the corresponding list is not configured, that counter stays 0 without an error.
func (c *Client) QueryOutboundStats(ctx context.Context) (OutboundBytes, error) {
	payload, err := grpcUnary(ctx, c.addr, queryStatsMethod, encodeQueryStatsReq("outbound>>>", true))
	if err != nil {
		return OutboundBytes{}, fmt.Errorf("QueryStats %s: %w", c.addr, err)
	}
	stats, err := decodeQueryStatsResp(payload)
	if err != nil {
		return OutboundBytes{}, fmt.Errorf("decode QueryStats: %w", err)
	}
	return ParseOutboundStats(stats), nil
}

// ParseOutboundStats aggregates the byte deltas of the direct and block outbounds from
// outbound>>><tag>>>traffic>>>uplink|downlink (summing each one's up and down; a blackhole outbound usually has only uplink).
func ParseOutboundStats(stats []Stat) OutboundBytes {
	var out OutboundBytes
	for _, s := range stats {
		parts := strings.SplitN(s.Name, ">>>", 4)
		if len(parts) != 4 || parts[0] != "outbound" || parts[2] != "traffic" {
			continue
		}
		switch parts[3] {
		case "uplink", "downlink":
			switch parts[1] {
			case directOutboundTag:
				out.Direct += s.Value
			case blockOutboundTag:
				out.Blocked += s.Value
			}
		}
	}
	return out
}

// Collector adapts the stats queries to the traffic package's collection rules (Collector + UserCollector).
type Collector struct {
	client *Client
}

// NewCollector creates a traffic collector of the xray kind.
func NewCollector(addr string, logger *log.Logger) *Collector {
	return &Collector{client: New(addr, logger)}
}

// Collect returns the node-level delta (the sum of all user deltas); conns has no source and is always 0.
func (c *Collector) Collect(proc string) (rx, tx uint64, conns int, err error) {
	users, err := c.CollectUsers(proc)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, u := range users {
		rx += u.Rx
		tx += u.Tx
	}
	return rx, tx, 0, nil
}

// CollectUsers returns the per-email traffic deltas within one query cycle.
func (c *Collector) CollectUsers(proc string) ([]agentproto.UserTraffic, error) {
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	byEmail, err := c.client.QueryUserStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]agentproto.UserTraffic, 0, len(byEmail))
	for email, u := range byEmail {
		if u.Rx == 0 && u.Tx == 0 {
			continue // a zero delta is never reported, keeping empty-line noise out
		}
		out = append(out, agentproto.UserTraffic{Email: email, Rx: u.Rx, Tx: u.Tx})
	}
	return out, nil
}

// CollectOutbound returns the direct and blocked outbound byte deltas within one query cycle (SAVE-4/7).
func (c *Collector) CollectOutbound(proc string) (direct, blocked uint64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	ob, err := c.client.QueryOutboundStats(ctx)
	return ob.Direct, ob.Blocked, err
}
