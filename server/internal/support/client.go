// Package support bridges the external customer-support service (servify): it issues session-bound
// guest tokens for panel users (the chat/feedback auth surface) and syncs user business context to the
// agent side as customer profile data for read-only display. Credential discipline: the service key is used server-side only;
// browsers hold only short-lived guest tokens, and a sync failure does not affect the main chat path (best-effort).
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

	"github.com/cuihairu/ferry/server/internal/ratelimit"
)

// Client is the minimal client for servify host-backend calls (embedding guide §3.2/§4.2).
type Client struct {
	// BaseURL is the servify service address (e.g. http://servify:8080).
	BaseURL string
	// ServiceKey is the X-API-Key (service principal), appearing only at this layer.
	ServiceKey string
	// HTTP defaults to a 5s timeout (support is a side path and must not block panel requests).
	HTTP *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// GuestToken issues a guest token bound to session_id (POST /api/v1/guest/session).
// The token does not require the session row to exist; its validity is configured on the servify side (default 24h).
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

// CustomerSnapshot is the customer business context synced to the agent side (read-only display).
type CustomerSnapshot struct {
	Email string // the servify side dedupes and locates by email (ferry users have no email, so a synthetic address is used)
	Name  string
	Notes string // a business snapshot overwritten once per call (quota/expiry/status), which the agent sees directly on the ticket page
}

// SyncCustomer locates the customer by email: overwrite notes if present, create a profile if absent;
// on a concurrent unique-constraint clash (409) it falls back to one re-query and then overwrites. All calls use server-side APIs (embedding guide §4.2).
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
		// Concurrent profile creation hit the unique constraint (409): fall back to one re-query and overwrite the existing profile.
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
		"username": snap.Email, // required by the servify side; the synthetic email is the unique key
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

// HTTPError is a call error carrying the status; isConflict uses it to recognize 409.
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
	// Pacing (F-0047): the shared per-host pacer bounds call bursts to the
	// servify API.
	if err := ratelimit.WaitURL(ctx, req.URL.String()); err != nil {
		return err
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
