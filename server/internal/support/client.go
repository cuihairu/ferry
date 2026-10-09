// Package support 对接外部客服服务（servify）：为面板用户签发绑定会话的
// 访客 token（聊天/反馈认证面），并把用户业务上下文以客户资料形式同步给
// 坐席侧只读展示。凭据纪律：service key 只在服务端使用，浏览器仅持短期
// 访客 token；同步失败不影响聊天主链路（best-effort）。
package support

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 servify 宿主后端调用的最小客户端（嵌入指南 §3.2/§4.2）。
type Client struct {
	// BaseURL 是 servify 服务地址（如 http://servify:8080）。
	BaseURL string
	// ServiceKey 是 X-API-Key（service principal），只在此层出现。
	ServiceKey string
	// HTTP 缺省 5s 超时（客服是旁路，不能拖住面板请求）。
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// GuestToken 签发绑定 session_id 的访客 token（POST /api/v1/guest/session）。
// token 不要求会话行已存在，有效期由 servify 侧配置决定（缺省 24h）。
func (c *Client) GuestToken(ctx context.Context, sessionID string) (token string, expiresAt int64, err error) {
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v1/guest/session", map[string]string{"session_id": sessionID}, &out); err != nil {
		return "", 0, err
	}
	return out.AccessToken, out.ExpiresAt, nil
}

// CustomerSnapshot 是同步给坐席侧的客户业务上下文（只读展示用）。
type CustomerSnapshot struct {
	Email string // servify 侧按邮箱去重定位（ferry 用户无邮箱，用合成地址）
	Name  string
	Notes string // 一次一覆盖的业务快照（配额/到期/状态），坐席在工单页直接看到
}

// SyncCustomer 按 email 定位客户：有则覆盖 notes，无则建档；并发撞唯一约束
// （409）时回落再查一次后覆盖。全程走服务端接口（嵌入指南 §4.2）。
func (c *Client) SyncCustomer(ctx context.Context, snap CustomerSnapshot) error {
	id, err := c.findCustomer(ctx, snap.Email)
	if err != nil {
		return err
	}
	if id == 0 {
		cerr := c.createCustomer(ctx, snap)
		if cerr == nil {
			return nil
		}
		if !isConflict(cerr) {
			return cerr
		}
		// 并发建档撞唯一约束（409）：回落重查一次按已有档案覆盖。
		rid, rerr := c.findCustomer(ctx, snap.Email)
		if rerr != nil {
			return rerr
		}
		if rid == 0 {
			return fmt.Errorf("customer create conflicted but not found on re-search: %w", cerr)
		}
		id = rid
	}
	return c.updateNotes(ctx, id, snap)
}

func (c *Client) findCustomer(ctx context.Context, email string) (uint64, error) {
	var out struct {
		Items []struct {
			ID    uint64 `json:"id"`
			Email string `json:"email"`
		} `json:"items"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/customers?search="+url.QueryEscape(email), nil, &out); err != nil {
		return 0, err
	}
	for _, it := range out.Items {
		if strings.EqualFold(it.Email, email) {
			return it.ID, nil
		}
	}
	return 0, nil
}

func (c *Client) createCustomer(ctx context.Context, snap CustomerSnapshot) error {
	body := map[string]string{
		"username": snap.Email, // servify 侧必填，合成邮箱即唯一键
		"email":    snap.Email,
		"name":     snap.Name,
		"source":   "ferry",
		"notes":    snap.Notes,
	}
	return c.call(ctx, http.MethodPost, "/api/customers", body, nil)
}

func (c *Client) updateNotes(ctx context.Context, id uint64, snap CustomerSnapshot) error {
	body := map[string]string{"notes": snap.Notes}
	return c.call(ctx, http.MethodPut, fmt.Sprintf("/api/customers/%d", id), body, nil)
}

// HTTPError 带 status 的调用错误，isConflict 靠它识别 409。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("servify %d: %s", e.Status, e.Body) }

func isConflict(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusConflict
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.ServiceKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
