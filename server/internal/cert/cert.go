// Package cert is the ACME orchestration for certificates (BR-4): the panel manages tasks, expiry and renewal
// triggers, while issuance reuses an external tool slot (acme.sh; certbot and others extend on demand later).
// DNS-01 reuses the BR-2 DNS provider credential channel: credentials reach the execution environment only via
// environment variables, never on the command line or in logs (same model as provisioning TF_VAR). Artifacts stay
// in the tool working directory; node distribution is picked up by a later batch.
package cert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Issuance methods and task state values.
const (
	MethodDNS01  = "dns-01"
	MethodHTTP01 = "http-01"

	StatePending = "pending"
	StateOK      = "ok"
	StateFailed  = "failed"
)

// RenewBefore is the pre-expiry renewal window: how many days before expiry a renewal is triggered.
const RenewBefore = 30 * 24 * time.Hour

// FailBackoff is the retry backoff after a failed issuance (avoids hammering the CA rate limit without backoff).
const FailBackoff = time.Hour

// MaxAttempts caps automatic issuance/renewal retries: a task that failed this many times stays
// failed until an admin retries (F-0042: background tasks need a retry ceiling, not infinite retry).
const MaxAttempts = 5

// IssueTimeout bounds one issuance/renewal run (acme.sh can hang on DNS propagation or CA
// outage; without a bound a stuck tool blocks the serial sweep, F-0042). Var so tests shorten it.
var IssueTimeout = 10 * time.Minute

// Runner executes one tool command: args are the command line, env the extra environment variables (credential channel).
type Runner func(ctx context.Context, args []string, env map[string]string) (string, error)

// Manager is the orchestration executor; a nil Run means real exec.
type Manager struct {
	Bin     string // acme.sh executable path (FERRY_ACME_BIN)
	Home    string // tool working directory (ACME_HOME, state centralized on the panel side)
	Webroot string // http-01 webroot（FERRY_ACME_WEBROOT）
	CA      string // ACME server (defaults to letsencrypt)
	Run     Runner
	// ReadExpiry reads a domain certificate's expiry (default openssl x509), injectable for tests.
	ReadExpiry func(domain string) (time.Time, error)
}

func (m *Manager) runner() Runner {
	if m.Run != nil {
		return m.Run
	}
	return m.realRun
}

// realRun executes the tool command under control: working directory Home, extra env merged into the environment, output combined and returned.
func (m *Manager) realRun(ctx context.Context, args []string, env map[string]string) (string, error) {
	if m.Bin == "" {
		m.Bin = "acme.sh"
	}
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	if m.Home != "" {
		cmd.Dir = m.Home
	}
	if len(env) > 0 {
		base := os.Environ()
		for k, v := range env {
			base = append(base, k+"="+v)
		}
		cmd.Env = base
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// dnsPlugin maps a DNS provider type to the acme.sh dns API plugin name (register one entry per added provider).
func dnsPlugin(providerType string) (string, error) {
	switch providerType {
	case "cloudflare":
		return "dns_cf", nil
	default:
		return "", fmt.Errorf("cert: dns provider type %q has no acme.sh plugin mapping", providerType)
	}
}

func (m *Manager) ca() string {
	if m.CA != "" {
		return m.CA
	}
	return "letsencrypt"
}

// Issue issues or reissues: domains[0] is the main domain, the rest become one -d each; env is the credential channel
// (required for dns-01, empty for http-01). Returns the combined output (kept as the task trail tail line).
func (m *Manager) Issue(ctx context.Context, domains []string, method, providerType string, env map[string]string) (string, error) {
	if len(domains) == 0 || domains[0] == "" {
		return "", errors.New("cert: domain is required")
	}
	args := []string{"--issue", "--server", m.ca()}
	for _, d := range domains {
		args = append(args, "-d", d)
	}
	switch method {
	case MethodDNS01:
		plugin, err := dnsPlugin(providerType)
		if err != nil {
			return "", err
		}
		args = append(args, "--dns", plugin)
	case MethodHTTP01:
		if m.Webroot == "" {
			return "", errors.New("cert: webroot not configured (set FERRY_ACME_WEBROOT)")
		}
		args = append(args, "--webroot", m.Webroot)
	default:
		return "", fmt.Errorf("cert: method %q not supported yet (dns-01/http-01)", method)
	}
	return m.runner()(ctx, args, env)
}

// Renew renews (acme.sh is an idempotent no-op when it judges the renewal window not yet reached).
func (m *Manager) Renew(ctx context.Context, domain string) (string, error) {
	if domain == "" {
		return "", errors.New("cert: domain is required")
	}
	return m.runner()(ctx, []string{"--renew", "-d", domain}, nil)
}

// Expiry reads a domain certificate's expiry: by default openssl parses the acme.sh artifact directory fullchain
// (<domain>_ecc/fullchain.cer); an unreadable expiry returns an error and the caller keeps the old value without blocking.
func (m *Manager) Expiry(domain string) (time.Time, error) {
	if m.ReadExpiry != nil {
		return m.ReadExpiry(domain)
	}
	cer := filepath.Join(m.Home, domain+"_ecc", "fullchain.cer")
	out, err := exec.Command("openssl", "x509", "-enddate", "-noout", "-in", cer).Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("cert: read expiry for %s: %w", domain, err)
	}
	line := strings.TrimSpace(string(out)) // notAfter=Mar  7 12:00:00 2026 GMT
	v, ok := strings.CutPrefix(line, "notAfter=")
	if !ok {
		return time.Time{}, fmt.Errorf("cert: unexpected openssl output: %q", line)
	}
	return time.Parse("Jan 2 15:04:05 2006 MST", v)
}
