// Package dns 是 DNS 商 API 插件位（BR-2 域名前置：域名不换、IP 随换）。
// 接口最小化到 upsert 单条记录：A 记录切换由恢复流水线 L1 驱动，
// TXT 记录留给 BR-4 ACME DNS-01 复用同一凭证与通道。
// 未知类型不冒称支持（口径同供给 provider）：录入经 KnownKind 校验，
// 执行经 New 报错，横扩一家=在 New 里加一个 case。
package dns

import (
	"context"
	"errors"
	"fmt"
)

// Provider 是一家 DNS 商的最小写通道。
type Provider interface {
	// Kind 是插件位类型名（与录入的 Type 对应）。
	Kind() string
	// Upsert 幂等地把 name 的 rtype 记录写为 value（有则改、无则建）。
	Upsert(ctx context.Context, name, rtype, value string) error
}

// kinds 是已接入的插件位类型（KnownKind/New 共用，加一家在此登记）。
var kinds = []string{"cloudflare"}

// KnownKind 校验类型是否已接入（录入层防冒称）。
func KnownKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// New 按类型造写通道；token 为解密后的明文凭证，不出方法边界。
func New(kind, token string) (Provider, error) {
	if token == "" {
		return nil, errors.New("dns: provider token not configured")
	}
	switch kind {
	case "cloudflare":
		return &Cloudflare{Token: token, Base: defaultBase}, nil
	default:
		return nil, fmt.Errorf("dns: provider type %q not supported yet (%v)", kind, kinds)
	}
}
