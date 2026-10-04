// Package xray 预留与 Xray 内核对接的接口。
// 当前里程碑仅定义边界，不做实际对接；后续可基于 gRPC API 实现此接口，
// 也可用空实现跑通纯订阅管理流程。
package xray

import "context"

// Handler 描述 ferry 对 Xray 内核所需的最小能力集合。
// 统计查询用于流量记账，出入站管理用于让节点配置生效。
type Handler interface {
	// QueryUserTraffic 返回指定用户标识自上次查询以来的累计收发字节数。
	// email 为 Xray 入站用户邮箱约定格式，由调用方负责拼接。
	QueryUserTraffic(ctx context.Context, email string) (rx, tx uint64, err error)
	// AddInbound 向内核应用一个入站配置，cfg 为 Xray inbound JSON 片段。
	AddInbound(ctx context.Context, cfg []byte) error
	// RemoveInbound 按 tag 移除入站。
	RemoveInbound(ctx context.Context, tag string) error
}

// NoopHandler 是不做任何事的空实现，供未对接内核的部署形态使用。
type NoopHandler struct{}

func (NoopHandler) QueryUserTraffic(context.Context, string) (uint64, uint64, error) {
	return 0, 0, nil
}
func (NoopHandler) AddInbound(context.Context, []byte) error    { return nil }
func (NoopHandler) RemoveInbound(context.Context, string) error { return nil }
