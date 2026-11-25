package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/golang-jwt/jwt/v5"
)

// TestAdminLoginSurfacesDisabled covers the off-by-default contract of G7: with
// neither provider configured the external surfaces answer 404 and the local
// password login stays the only entry point.
func TestAdminLoginSurfacesDisabled(t *testing.T) {
	r := newTestRouter(t)

	avail := doBare(t, r, "GET", "/admin/login/available")
	if avail.Code != http.StatusOK {
		t.Fatalf("available: %d %s", avail.Code, avail.Body)
	}
	var av struct {
		Password bool `json:"password"`
		Oidc     bool `json:"oidc"`
		Ldap     bool `json:"ldap"`
	}
	if err := json.Unmarshal(avail.Body.Bytes(), &av); err != nil {
		t.Fatalf("decode available: %v", err)
	}
	if !av.Password || av.Oidc || av.Ldap {
		t.Fatalf("available flags = %+v", av)
	}

	if rec := doBare(t, r, "GET", "/admin/oidc/login"); rec.Code != http.StatusNotFound {
		t.Fatalf("oidc login while disabled = %d", rec.Code)
	}
	if rec := doRaw(t, r, "POST", "/admin/login/ldap", `{"username":"admin","password":"pw"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("ldap login while disabled = %d", rec.Code)
	}
}

// TestOidcAdminLogin walks the authorization-code flow end to end against a mock
// provider: the redirect carries a signed state, the callback trades the code,
// resolves the provider identity to an admin account and issues the same admin
// JWT as the local login.
func TestOidcAdminLogin(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/token":
			if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:cs")); req.Header.Get("Authorization") != want {
				t.Errorf("token client auth = %q", req.Header.Get("Authorization"))
			}
			if got := req.FormValue("grant_type"); got != "authorization_code" {
				t.Errorf("grant_type = %q", got)
			}
			_, _ = w.Write([]byte(`{"access_token":"at-1","token_type":"Bearer"}`))
		case "/userinfo":
			if got := req.Header.Get("Authorization"); got != "Bearer at-1" {
				t.Errorf("userinfo auth = %q", got)
			}
			_, _ = w.Write([]byte(`{"preferred_username":"admin","email":"a@example.com","sub":"s-1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()

	r, db := newTestRouterCfg(t, func(c *config.Config) {
		c.Oidc = config.OidcConfig{
			Enabled: true, ClientID: "cid", ClientSecret: "cs",
			AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token",
			UserInfoURL: provider.URL + "/userinfo", BaseURL: provider.URL,
		}
	})
	seedAdmin(t, db, "admin", "pw")

	rec := doBare(t, r, "GET", "/admin/oidc/login")
	if rec.Code != http.StatusFound {
		t.Fatalf("oidc login: %d %s", rec.Code, rec.Body)
	}
	target, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := target.Query()
	if target.Path != "/authorize" || q.Get("client_id") != "cid" || q.Get("response_type") != "code" ||
		q.Get("redirect_uri") != provider.URL+"/admin/oidc/callback" || q.Get("state") == "" {
		t.Fatalf("authorize url = %s", target)
	}
	state := q.Get("state")

	// a tampered state must be refused before any provider round-trip
	if rec := doBare(t, r, "GET", "/admin/oidc/callback?code=c-1&state=tampered"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state = %d %s", rec.Code, rec.Body)
	}

	cb := doBare(t, r, "GET", "/admin/oidc/callback?code=c-1&state="+url.QueryEscape(state))
	if cb.Code != http.StatusOK {
		t.Fatalf("oidc callback: %d %s", cb.Code, cb.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(cb.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("callback body = %s", cb.Body)
	}
	if err := assertAdminJWT(t, out.Token, "admin"); err != nil {
		t.Fatalf("%v", err)
	}

	// a return target off this origin is dropped (a "//host" value would become
	// an open redirect), so the callback falls back to the JSON answer
	rec = doBare(t, r, "GET", "/admin/oidc/login?redirect=//evil.example.com/x")
	if rec.Code != http.StatusFound {
		t.Fatalf("oidc login: %d", rec.Code)
	}
	target, err = url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	state = target.Query().Get("state")
	cb = doBare(t, r, "GET", "/admin/oidc/callback?code=c-1&state="+url.QueryEscape(state))
	if cb.Code != http.StatusOK || cb.Header().Get("Location") != "" {
		t.Fatalf("off-origin return target answered %d %q", cb.Code, cb.Header().Get("Location"))
	}
	for _, p := range []string{"/nodes", "/a/b", "/x?y=1"} {
		if got := sameOriginPath(p); got != p {
			t.Fatalf("sameOriginPath(%q) = %q", p, got)
		}
	}
	for _, p := range []string{"//evil.example.com/x", "https://evil.example.com", "", "nodes", "javascript:alert(1)"} {
		if got := sameOriginPath(p); got != "" {
			t.Fatalf("sameOriginPath(%q) = %q, want empty", p, got)
		}
	}

	avail := doBare(t, r, "GET", "/admin/login/available")
	if b := avail.Body.String(); !strings.Contains(b, `"oidc":true`) {
		t.Fatalf("available flags = %s", b)
	}
}

// TestLdapAdminLogin covers the directory bind login (G7): an accepted bind
// issues the admin JWT for the matching admin account, a rejected bind and a
// non-admin identity both fail closed.
func TestLdapAdminLogin(t *testing.T) {
	r, db := newTestRouterCfg(t, func(c *config.Config) {
		c.Ldap = config.LdapConfig{
			Enabled: true, URL: "ldap://" + ldapListen(t, 0).String(),
			BindDN: "uid=%s,ou=people,dc=example,dc=com", TimeoutSec: 3,
		}
	})
	seedAdmin(t, db, "admin", "pw")
	seedAdmin(t, db, "guest", "pw")
	db.Model(&storage.User{}).Where("username = ?", "guest").Update("is_admin", false)

	body := `{"username":"admin","password":"pw"}`
	rec := doRaw(t, r, "POST", "/admin/login/ldap", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("ldap login: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("ldap login body = %s", rec.Body)
	}
	if err := assertAdminJWT(t, out.Token, "admin"); err != nil {
		t.Fatalf("%v", err)
	}

	// a directory that rejects the credentials fails closed
	rejected := ldapListen(t, 49)
	rBad, _ := newTestRouterCfg(t, func(c *config.Config) {
		c.Ldap = config.LdapConfig{Enabled: true, URL: "ldap://" + rejected.String(),
			BindDN: "uid=%s,ou=people", TimeoutSec: 3}
	})
	if rec := doRaw(t, rBad, "POST", "/admin/login/ldap", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rejected bind = %d %s", rec.Code, rec.Body)
	}

	// the directory accepted the credentials, but the account is not an admin
	if rec := doRaw(t, r, "POST", "/admin/login/ldap", `{"username":"guest","password":"pw"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("non admin bind = %d %s", rec.Code, rec.Body)
	}
}

// ldapListen serves a single canned bind result code on a loopback port
// (0 = success, 49 = invalid credentials) -- the smallest directory answer
// go-ldap can decode.
func ldapListen(t *testing.T, result byte) net.Addr {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				buf := make([]byte, 512)
				n, err := conn.Read(buf)
				if err != nil || n < 5 {
					return
				}
				idx := 2
				if buf[1]&0x80 != 0 {
					idx = 2 + int(buf[1]&0x7f)
				}
				resp := []byte{0x30, 0x0c, 0x02, 0x01, buf[idx+2],
					0x61, 0x07, 0x0a, 0x01, result, 0x04, 0x00, 0x04, 0x00}
				_, _ = conn.Write(resp)
			}(conn)
		}
	}()
	return ln.Addr()
}

// assertAdminJWT decodes a login token with the default admin secret and checks
// it is an admin session for the given account.
func assertAdminJWT(t *testing.T, token, username string) error {
	t.Helper()
	claims := &adminClaims{}
	tkn, err := jwt.ParseWithClaims(token, claims, func(_ *jwt.Token) (interface{}, error) {
		return []byte("ferry-admin-secret"), nil
	})
	if err != nil || !tkn.Valid {
		return fmt.Errorf("token %q: %v", token, err)
	}
	if !claims.IsAdmin || claims.Subject != username {
		return fmt.Errorf("token claims = %+v", claims)
	}
	return nil
}
