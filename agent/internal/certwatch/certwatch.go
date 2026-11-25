// Package certwatch collects certificate expiry info from managed processes' core configs (A-3)
// and defines the expiry-decision rule (A-7). Core config shapes vary (xray/sing-box/hysteria2), so it
// mines by the "key name containing cert -> take the value" heuristic: a PEM text value is parsed directly,
// otherwise it is read as a file path; unreadable or non-certificate values are skipped silently and never affect the heartbeat.
package certwatch

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// ExpiryWindow is the certificate expiry warning window: cert_expiry alarms fire inside it (expired certificates included).
const ExpiryWindow = 30 * 24 * time.Hour

// FromFile parses one core config file and returns the expiry state of the certificates readable from it.
func FromFile(path string) []agentproto.CertStatus {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []agentproto.CertStatus
	collect(tree, &out, map[string]bool{})
	return out
}

// collect walks the config tree deeply and extracts the certificates pointed at by cert-like keys.
func collect(v any, out *[]agentproto.CertStatus, seen map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok && strings.Contains(strings.ToLower(k), "cert") {
				if cs, ok := parseSource(s); ok {
					key := cs.Domain + "@" + cs.NotAfter.Format(time.RFC3339)
					if !seen[key] {
						seen[key] = true
						*out = append(*out, cs)
					}
				}
			}
			collect(child, out, seen)
		}
	case []any:
		for _, child := range t {
			collect(child, out, seen)
		}
	}
}

func parseSource(s string) (agentproto.CertStatus, bool) {
	trimmed := strings.TrimSpace(s)
	if strings.Contains(trimmed, "-----BEGIN") {
		return parsePEM([]byte(trimmed))
	}
	raw, err := os.ReadFile(trimmed)
	if err != nil {
		return agentproto.CertStatus{}, false
	}
	return parsePEM(raw)
}

func parsePEM(raw []byte) (agentproto.CertStatus, bool) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return agentproto.CertStatus{}, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return agentproto.CertStatus{}, false
	}
	return agentproto.CertStatus{Domain: cert.Subject.CommonName, NotAfter: cert.NotAfter}, true
}

// Expiring filters the certificates expiring within window (expired ones included).
func Expiring(certs []agentproto.CertStatus, now time.Time, window time.Duration) []agentproto.CertStatus {
	var out []agentproto.CertStatus
	for _, c := range certs {
		if c.NotAfter.Sub(now) <= window {
			out = append(out, c)
		}
	}
	return out
}
