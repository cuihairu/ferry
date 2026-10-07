// Package xraystats 对接 xray gRPC stats API 采集真实用户流量（P1-3，
// 来源：Marzban xray_api/stats.py QueryStats）。
// 客户端只读 QueryStats：pattern=user>>>、reset=true，拿到自上次查询
// 的 per-email 上下行增量；节点本地直连（api 只暴露在 127.0.0.1）。
package xraystats

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// queryStatsMethod 是 StatsService 的 QueryStats 全名（xray 官方 proto）。
const queryStatsMethod = "/xray.app.stats.command.StatsService/QueryStats"

// collectTimeout 是单次 stats 查询超时。
const collectTimeout = 5 * time.Second

// UserBytes 是一个用户的上下行字节增量。
type UserBytes struct {
	Rx uint64 // 用户收（downlink）
	Tx uint64 // 用户发（uplink）
}

// Client 是 xray gRPC stats 客户端：每次查询现建连接（周期采集间隔秒级，
// 免重连状态机，xray 重启后自愈）。
type Client struct {
	addr string // xray api 监听，如 127.0.0.1:10085
	log  *log.Logger

	dialOpts []grpc.DialOption // 测试注入钩子（bufconn 等）
}

// New 创建客户端。
func New(addr string, logger *log.Logger) *Client {
	if logger == nil {
		logger = log.Default()
	}
	return &Client{addr: addr, log: logger}
}

// newWithDial 供测试注入拨号选项。
func newWithDial(addr string, logger *log.Logger, opts ...grpc.DialOption) *Client {
	c := New(addr, logger)
	c.dialOpts = opts
	return c
}

// QueryUserStats 查询并清零全部用户统计，返回 per-email 上下行增量。
// xray 侧需开启 per-user 统计（policy.levels[].statsUserUplink/Downlink
// 与 stats service/api inbound，部署侧口径）；未开启时返回空表不报错。
func (c *Client) QueryUserStats(ctx context.Context) (map[string]UserBytes, error) {
	conn, err := grpc.NewClient(c.addr,
		append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
			c.dialOpts...)...)
	if err != nil {
		return nil, fmt.Errorf("dial xray stats %s: %w", c.addr, err)
	}
	defer conn.Close()

	resp := &QueryStatsResponse{}
	err = conn.Invoke(ctx, queryStatsMethod, &QueryStatsRequest{Pattern: "user>>>", Reset_: true}, resp)
	if err != nil {
		return nil, fmt.Errorf("QueryStats: %w", err)
	}
	return ParseUserStats(resp.Stat), nil
}

// ParseUserStats 把 QueryStats 结果聚合为 per-email 上下行：
// name 形如 user>>>EMAIL>>>traffic>>>uplink|downlink，其余条目忽略。
// 口径对齐 traffic_logs 用户视角：downlink=收（rx），uplink=发（tx）。
func ParseUserStats(stats []*Stat) map[string]UserBytes {
	out := map[string]UserBytes{}
	for _, s := range stats {
		if s == nil {
			continue
		}
		parts := strings.SplitN(s.Name, ">>>", 4)
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		var u UserBytes
		switch parts[3] {
		case "downlink":
			u = out[parts[1]]
			u.Rx += uint64(s.Value)
		case "uplink":
			u = out[parts[1]]
			u.Tx += uint64(s.Value)
		default:
			continue
		}
		out[parts[1]] = u
	}
	return out
}

// Collector 把 stats 查询适配为 traffic 包的采集口径（Collector + UserCollector）。
type Collector struct {
	client *Client
}

// NewCollector 创建 xray kind 的流量采集器。
func NewCollector(addr string, logger *log.Logger) *Collector {
	return &Collector{client: New(addr, logger)}
}

// Collect 返回节点级增量（全部用户增量求和）；conns 无来源恒 0。
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

// CollectUsers 返回一次查询周期内 per-email 的流量增量。
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
			continue // 零增量不进上报，避免空行噪音
		}
		out = append(out, agentproto.UserTraffic{Email: email, Rx: u.Rx, Tx: u.Tx})
	}
	return out, nil
}
