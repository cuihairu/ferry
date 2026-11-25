// Package idp is the external admin login providers of the admin panel (G7):
// an OIDC/OAuth2 authorization-code client and an LDAP simple-bind client.
// Both are off unless explicitly enabled in the config, so the local password
// login path stays the default and untouched. The OIDC exchange is plain
// standard-library HTTP (authorization code + userinfo JSON, no discovery --
// the endpoints are configured explicitly); LDAP uses go-ldap/ldap/v3 (MIT).
package idp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/ratelimit"
)

// OIDC is the authorization-code client of one provider.
type OIDC struct {
	cfg    config.OidcConfig
	client *http.Client
}

// NewOIDC builds the client. A non-nil client is returned even when disabled (the
// handler surface decides availability, so config mistakes surface as 501-style
// errors rather than nil dereferences).
func NewOIDC(cfg config.OidcConfig) *OIDC {
	return &OIDC{cfg: cfg, client: &http.Client{Timeout: 10 * time.Second}}
}

// RedirectURL resolves the callback address: the explicit setting, or
// <BaseURL>/admin/oidc/callback.
func (o *OIDC) RedirectURL() string {
	if o.cfg.RedirectURL != "" {
		return o.cfg.RedirectURL
	}
	return strings.TrimRight(o.cfg.BaseURL, "/") + "/admin/oidc/callback"
}

// AuthorizeURL renders the provider's authorize redirect for the given state
// (the state is produced and verified by the handler, this side only carries it).
func (o *OIDC) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", o.cfg.ClientID)
	q.Set("redirect_uri", o.RedirectURL())
	q.Set("response_type", "code")
	q.Set("scope", o.cfg.Scope)
	q.Set("state", state)
	sep := "?"
	if strings.Contains(o.cfg.AuthURL, "?") {
		sep = "&"
	}
	return o.cfg.AuthURL + sep + q.Encode()
}

// Identity is the provider's answer for one authenticated person: the login
// name ferry resolves to an admin account.
type Identity struct {
	Username string
	Email    string
	Subject  string
}

// Exchange trades the authorization code for an access token and reads the
// userinfo endpoint. The token request follows the standard OAuth2 form post
// with HTTP Basic client authentication; userinfo is a Bearer GET.
func (o *OIDC) Exchange(code string) (Identity, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.RedirectURL())
	req, err := http.NewRequest(http.MethodPost, o.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("oidc token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(o.cfg.ClientID), url.QueryEscape(o.cfg.ClientSecret))
	// Pacing (F-0047): the shared per-host pacer bounds login-driven bursts to
	// the IdP.
	if err := ratelimit.WaitURL(context.Background(), o.cfg.TokenURL); err != nil {
		return Identity{}, fmt.Errorf("oidc token pacing: %w", err)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("oidc token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("oidc token endpoint: status %d: %s", resp.StatusCode, string(body))
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return Identity{}, fmt.Errorf("oidc token decode: %w", err)
	}
	if token.AccessToken == "" {
		return Identity{}, errors.New("oidc token response has no access_token")
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}

	req, err = http.NewRequest(http.MethodGet, o.cfg.UserInfoURL, nil)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc userinfo request: %w", err)
	}
	req.Header.Set("Authorization", token.TokenType+" "+token.AccessToken)
	if err := ratelimit.WaitURL(context.Background(), o.cfg.UserInfoURL); err != nil {
		return Identity{}, fmt.Errorf("oidc userinfo pacing: %w", err)
	}
	resp, err = o.client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("oidc userinfo endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("oidc userinfo response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("oidc userinfo endpoint: status %d: %s", resp.StatusCode, string(body))
	}
	var info struct {
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		Subject           string `json:"sub"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return Identity{}, fmt.Errorf("oidc userinfo decode: %w", err)
	}
	if info.PreferredUsername == "" && info.Subject == "" {
		return Identity{}, errors.New("oidc userinfo has neither preferred_username nor sub")
	}
	return Identity{Username: info.PreferredUsername, Email: info.Email, Subject: info.Subject}, nil
}
