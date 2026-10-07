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
)

const defaultBase = "https://api.cloudflare.com/client/v4"

// Cloudflare 走 Cloudflare API（Bearer Token）：zone 从记录名逐级向上定位，
// 记录 upsert。proxied 状态随既有记录保持（橙云不因切换被改写）。
// Base 可注入（测试指到 httptest），默认官方 API。
type Cloudflare struct {
	Token string
	Base  string
	// HTTP 缺省 10s 超时：切换是恢复流水线 L1 的秒级动作，不拖扫表。
	HTTP *http.Client
}

func (c *Cloudflare) Kind() string { return "cloudflare" }

// cfResp 是 Cloudflare API 统一响应壳。
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

// do 发一次 GET/PUT/POST 并解出 result；非 2xx/!success 报首条错误。
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

// findZone 从记录名逐级向上定位托管 zone（a.b.example.com → example.com）。
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

// Upsert 把 name 的 rtype 记录写为 value：命中则改（保持 proxied），未命中则建。
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
	payload["ttl"] = 1 // 自动
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), payload)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
