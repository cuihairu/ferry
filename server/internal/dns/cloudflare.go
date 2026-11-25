package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/cuihairu/ferry/server/internal/safestr"
)

const defaultBase = "https://api.cloudflare.com/client/v4"

// Cloudflare goes through the Cloudflare API (Bearer Token): the zone is located by walking
// up level by level from the record name, then the record is upserted. The proxied state is kept as the
// existing record has it (the orange cloud is not rewritten by a switch).
type Cloudflare struct {
	Token string
	Base  string
	// HTTP defaults to a 10s timeout: switching is a second-scale action in recovery tier L1 and must not drag down the scan.
	HTTP *http.Client
}

func (c *Cloudflare) Kind() string { return "cloudflare" }

// cfResp is the Cloudflare API's uniform response envelope.
type cfResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *Cloudflare) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Cloudflare) base() string {
	if c.Base != "" {
		return c.Base
	}
	return defaultBase
}

// do issues one GET/PUT/POST and extracts result; non-2xx / !success reports the first error.
func (c *Cloudflare) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Pacing (F-0047): Cloudflare enforces API quotas; the shared per-host pacer
	// keeps record ops well under them.
	if err := ratelimit.WaitURL(ctx, req.URL.String()); err != nil {
		return nil, err
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var out cfResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("cloudflare: http %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !out.Success {
		msg := fmt.Sprintf("http %d", resp.StatusCode)
		if len(out.Errors) > 0 && out.Errors[0].Message != "" {
			msg += ": " + out.Errors[0].Message
		}
		return nil, fmt.Errorf("cloudflare: %s", msg)
	}
	return out.Result, nil
}

// findZone walks up level by level from the record name to locate the hosted zone (a.b.example.com -> example.com).
func (c *Cloudflare) findZone(ctx context.Context, fqdn string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(fqdn)), ".")
	if name == "" {
		return "", fmt.Errorf("cloudflare: empty record name")
	}
	for {
		result, err := c.do(ctx, http.MethodGet, "/zones?name="+name, nil)
		if err != nil {
			return "", err
		}
		var zones []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(result, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 && zones[0].ID != "" {
			return zones[0].ID, nil
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return "", fmt.Errorf("cloudflare: no zone found for %q", fqdn)
		}
		name = name[i+1:]
	}
}

// Upsert writes name's rtype record to value: on a hit it is changed (keeping proxied), otherwise created.
func (c *Cloudflare) Upsert(ctx context.Context, name, rtype, value string) error {
	zoneID, err := c.findZone(ctx, name)
	if err != nil {
		return err
	}
	result, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, rtype, name), nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID      string `json:"id"`
		Proxied bool   `json:"proxied"`
	}
	if err := json.Unmarshal(result, &records); err != nil {
		return err
	}
	payload := map[string]any{"type": rtype, "name": name, "content": value}
	if len(records) > 0 {
		payload["proxied"] = records[0].Proxied
		_, err = c.do(ctx, http.MethodPut,
			fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, records[0].ID), payload)
		return err
	}
	payload["ttl"] = 1 // automatic
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), payload)
	return err
}

// Delete removes all rtype records for name (E-27 full-outage record withdrawal); no records means plain success.
func (c *Cloudflare) Delete(ctx context.Context, name, rtype string) error {
	zoneID, err := c.findZone(ctx, name)
	if err != nil {
		return err
	}
	result, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, rtype, name), nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result, &records); err != nil {
		return err
	}
	for _, r := range records {
		if _, err := c.do(ctx, http.MethodDelete,
			fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, r.ID), nil); err != nil {
			return err
		}
	}
	return nil
}

// truncate takes the prefix of an error summary and appends an ellipsis: the cut point is walked
// back to a UTF-8 character boundary (error text may contain Chinese, a hard byte cut would split a rune).
func truncate(s string, n int) string {
	return safestr.HeadEllipsis(s, n)
}
