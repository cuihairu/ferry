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

// DNSAction is the L1 domain-front switch (BR-2): the front domain's A record
// moves onto a rotating backup IP (domain unchanged, IP swapped, seconds scale);
// Restore switches back to normal on probe-recovery finish. With no domain front configured it fails with a reason and the orchestration advances (as with L3 and no template).
type DNSAction struct {
	DB    *gorm.DB
	Store *secret.Store
	// New is the plugin-slot factory (defaults to dns.New); tests may inject a fake channel.
	New func(kind, token string) (dns.Provider, error)
}

// Name is the action trace name (Recovery.Action).
func (a *DNSAction) Name() string { return "dns_switch" }

func (a *DNSAction) factory() func(kind, token string) (dns.Provider, error) {
	if a.New != nil {
		return a.New
	}
	return dns.New
}

// providerFor resolves a DNS provider's write channel; credential decryption only happens inside method boundaries.
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

// switchable returns the operable front records with their credential rows (only enabled DNS providers are taken).
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

// Run switches away every front record not yet switched: take the next backup
// IP by the rotation cursor, and only record switched once the upsert succeeded (no half state on failure; the orchestration advances on failure).
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

// Restore switches the switched-away front records back to normal (the
// probe-recovery finish hook): back to PrimaryIP with switched cleared; a no-op when there are no switched-away records.
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

// parseBackups parses the backup IP list (JSON string array), skipping empty strings.
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
