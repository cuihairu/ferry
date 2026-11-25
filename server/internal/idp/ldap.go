package idp

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	ldap "github.com/go-ldap/ldap/v3"
)

// LDAP is a per-user simple bind against a directory (G7): a login attempt binds
// as the configured DN template of the username with the supplied password, so
// the directory itself is the credential authority.
type LDAP struct {
	cfg config.LdapConfig
}

// NewLDAP builds the client from the config; a zero timeout falls back to five
// seconds so an unreachable directory can never hang a login attempt.
func NewLDAP(cfg config.LdapConfig) *LDAP {
	if cfg.TimeoutSec <= 0 {
		cfg.TimeoutSec = 5
	}
	return &LDAP{cfg: cfg}
}

// Auth binds with the given credentials and reports whether the directory
// accepted them. Distinct errors come back for a misconfigured template, an
// unreachable directory, a rejected bind and a bind that timed out.
func (l *LDAP) Auth(username, password string) error {
	if !strings.Contains(l.cfg.BindDN, "%s") {
		return errors.New("ldap bind dn template must contain %s")
	}
	dn := fmt.Sprintf(l.cfg.BindDN, username)
	timeout := time.Duration(l.cfg.TimeoutSec) * time.Second
	conn, err := l.connect()
	if err != nil {
		return err
	}
	defer conn.Close()
	// The request timeout bounds the bind answer: a directory that accepts the
	// socket but never replies must not hold the login endpoint either.
	conn.SetTimeout(timeout)
	if err := conn.Bind(dn, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return fmt.Errorf("ldap invalid credentials")
		}
		if strings.Contains(strings.ToLower(err.Error()), "timeout") ||
			ldap.IsErrorWithCode(err, ldap.LDAPResultBusy) {
			return fmt.Errorf("ldap bind timed out after %s", timeout)
		}
		return fmt.Errorf("ldap bind: %w", err)
	}
	return nil
}

// connect opens the directory connection per the configured URL scheme
// (ldaps:// for direct TLS) and upgrades a plain one with STARTTLS when asked.
func (l *LDAP) connect() (*ldap.Conn, error) {
	dialer := net.Dialer{Timeout: time.Duration(l.cfg.TimeoutSec) * time.Second}
	if l.cfg.StartTLS {
		// STARTTLS upgrades a plain socket; ldaps:// would already be TLS and
		// the two cannot be combined.
		if strings.HasPrefix(strings.ToLower(l.cfg.URL), "ldaps:") {
			return nil, errors.New("ldap start_tls conflicts with an ldaps:// url")
		}
		conn, err := ldap.DialURL("ldap://"+hostOf(l.cfg.URL), ldap.DialWithDialer(&dialer))
		if err != nil {
			return nil, fmt.Errorf("ldap connect: %w", err)
		}
		if err := conn.StartTLS(nil); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ldap start_tls: %w", err)
		}
		return conn, nil
	}
	conn, err := ldap.DialURL(l.cfg.URL, ldap.DialWithDialer(&dialer))
	if err != nil {
		return nil, fmt.Errorf("ldap connect: %w", err)
	}
	return conn, nil
}

// hostOf strips the scheme and any credentials for the STARTTLS dial address.
func hostOf(u string) string {
	trimmed := strings.TrimSpace(u)
	for _, scheme := range []string{"ldaps://", "ldap://"} {
		if strings.HasPrefix(strings.ToLower(trimmed), scheme) {
			trimmed = trimmed[len(scheme):]
			break
		}
	}
	if at := strings.LastIndex(trimmed, "@"); at >= 0 {
		trimmed = trimmed[at+1:]
	}
	return strings.TrimRight(trimmed, "/")
}
