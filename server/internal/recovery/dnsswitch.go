package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// DNSAction 是 L1 域名前置切换（BR-2）：前置域名 A 记录切到备用 IP 轮换
// （域名不换、IP 随换，秒级），探测恢复收尾经 Restore 回切常态。
// 未配置域名前置时失败留原因，编排推进（口径同 L3 未配置模板）。
type DNSAction struct {
	DB    *gorm.DB
	Store *secret.Store
	// New 是插件位工厂（缺省 dns.New），测试可注入假通道。
	New func(kind, token string) (dns.Provider, error)
}

// Name 是动作留痕名（Recovery.Action）。
func (a *DNSAction) Name() string { return "dns_switch" }

func (a *DNSAction) factory() func(kind, token string) (dns.Provider, error) {
	if a.New != nil {
		return a.New
	}
	return dns.New
}

// providerFor 解出一家 DNS 商的写通道；凭证解密只在方法边界内出现。
func (a *DNSAction) providerFor(prow storage.DNSProvider) (dns.Provider, error) {
	if a.Store == nil || !a.Store.Enabled() {
		return nil, errors.New("recovery: secret master key not configured (set FERRY_SECRET_KEY)")
	}
	token, err := a.Store.Decrypt(prow.APIKey)
	if err != nil {
		return nil, fmt.Errorf("recovery: dns provider %s: %w", prow.Name, err)
	}
	return a.factory()(prow.Type, token)
}

// switchable 返回可操作的前置记录与其凭证行（只取 enabled 的 DNS 商）。
func (a *DNSAction) switchable(switched bool) ([]storage.DNSFront, []storage.DNSProvider, error) {
	var provs []storage.DNSProvider
	if err := a.DB.Where("enabled = ?", true).Find(&provs).Error; err != nil {
		return nil, nil, err
	}
	byID := make(map[uint]storage.DNSProvider, len(provs))
	ids := make([]uint, 0, len(provs))
	for _, p := range provs {
		byID[p.ID] = p
		ids = append(ids, p.ID)
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}
	var fronts []storage.DNSFront
	if err := a.DB.Where("provider_id IN ? AND switched = ?", ids, switched).
		Find(&fronts).Error; err != nil {
		return nil, nil, err
	}
	return fronts, provs, nil
}

// Run 切走全部未切离的前置记录：按备用 IP 轮换游标取下一个，
// upsert 成功才落 switched（失败不留半态，编排按失败推进）。
func (a *DNSAction) Run(ctx context.Context, node storage.Node) error {
	fronts, provs, err := a.switchable(false)
	if err != nil {
		return err
	}
	if len(fronts) == 0 {
		return errors.New("recovery: dns front not configured")
	}
	byID := make(map[uint]storage.DNSProvider, len(provs))
	for _, p := range provs {
		byID[p.ID] = p
	}
	for _, f := range fronts {
		ips, err := parseBackups(f.BackupIPs)
		if err != nil {
			return fmt.Errorf("recovery: dns front %s: %w", f.Domain, err)
		}
		if len(ips) == 0 {
			return fmt.Errorf("recovery: dns front %s: no backup ips", f.Domain)
		}
		prow, ok := byID[f.ProviderID]
		if !ok {
			return fmt.Errorf("recovery: dns front %s: provider %d missing or disabled", f.Domain, f.ProviderID)
		}
		p, err := a.providerFor(prow)
		if err != nil {
			return err
		}
		ip := ips[f.SwitchIndex%len(ips)]
		if err := p.Upsert(ctx, f.Domain, "A", ip); err != nil {
			return fmt.Errorf("recovery: dns switch %s: %w", f.Domain, err)
		}
		if err := a.DB.Model(&storage.DNSFront{}).Where("id = ?", f.ID).
			Updates(map[string]any{"switched": true, "current_ip": ip,
				"switch_index": f.SwitchIndex + 1}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Restore 把已切离的前置记录回切常态（探测恢复收尾钩子）：
// 回到 PrimaryIP 并清 switched；没有切离记录时是空操作。
func (a *DNSAction) Restore(ctx context.Context) error {
	fronts, provs, err := a.switchable(true)
	if err != nil {
		return err
	}
	if len(fronts) == 0 {
		return nil
	}
	byID := make(map[uint]storage.DNSProvider, len(provs))
	for _, p := range provs {
		byID[p.ID] = p
	}
	for _, f := range fronts {
		prow, ok := byID[f.ProviderID]
		if !ok {
			return fmt.Errorf("recovery: dns restore %s: provider %d missing or disabled", f.Domain, f.ProviderID)
		}
		p, err := a.providerFor(prow)
		if err != nil {
			return err
		}
		if err := p.Upsert(ctx, f.Domain, "A", f.PrimaryIP); err != nil {
			return fmt.Errorf("recovery: dns restore %s: %w", f.Domain, err)
		}
		if err := a.DB.Model(&storage.DNSFront{}).Where("id = ?", f.ID).
			Updates(map[string]any{"switched": false, "current_ip": f.PrimaryIP}).Error; err != nil {
			return err
		}
	}
	return nil
}

// parseBackups 解析备用 IP 列表（JSON 字符串数组），跳过空串。
func parseBackups(raw string) ([]string, error) {
	var ips []string
	if raw == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(raw), &ips); err != nil {
		return nil, fmt.Errorf("backup_ips must be a JSON string array: %w", err)
	}
	out := ips[:0]
	for _, ip := range ips {
		if ip != "" {
			out = append(out, ip)
		}
	}
	return out, nil
}
