// Package certwatch 从受管进程的核心配置中收集证书到期信息（A-3），
// 并给出临期判定口径（A-7）。核心配置形态各异（xray/sing-box/hysteria2），
// 这里按「键名含 cert → 取值」启发式挖掘：值为 PEM 文本直接解析，否则视作文件路径读取；
// 读不到或非证书一律静默跳过，不影响心跳。
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

// ExpiryWindow 证书到期预警窗口：窗口内（含已过期）触发 cert_expiry 告警。
const ExpiryWindow = 30 * 24 * time.Hour

// FromFile 解析一个核心配置文件，返回其中可读证书的到期状态。
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

// collect 深度遍历配置树，抽取 cert 相关键指向的证书。
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

// Expiring 过滤出 window 内到期（含已过期）的证书。
func Expiring(certs []agentproto.CertStatus, now time.Time, window time.Duration) []agentproto.CertStatus {
	var out []agentproto.CertStatus
	for _, c := range certs {
		if c.NotAfter.Sub(now) <= window {
			out = append(out, c)
		}
	}
	return out
}
