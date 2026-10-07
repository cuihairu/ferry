// Package cert 是证书 ACME 编排（BR-4）：面板管任务、到期与续期触发，
// 签发执行复用外部工具位（acme.sh；certbot 等后续按需横扩）。
// DNS-01 复用 BR-2 的 DNS 商凭证通道：凭证只经环境变量进执行环境，
// 不落命令行与日志（口径同供给 TF_VAR）。产物留在工具工作目录，
// 节点分发由后续批次接。
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

// 签发方式与任务状态取值。
const (
	MethodDNS01  = "dns-01"
	MethodHTTP01 = "http-01"

	StatePending = "pending"
	StateOK      = "ok"
	StateFailed  = "failed"
)

// RenewBefore 是到期续期窗口：证书剩多少天内触发续期。
const RenewBefore = 30 * 24 * time.Hour

// FailBackoff 是签发失败后的重试退避（避免无退避打爆 CA 限频）。
const FailBackoff = time.Hour

// Runner 是一次工具命令执行：args 命令行、env 附加环境变量（凭证通道）。
type Runner func(ctx context.Context, args []string, env map[string]string) (string, error)

// Manager 是编排执行器；Run 空=真实 exec。
type Manager struct {
	Bin     string // acme.sh 可执行路径（FERRY_ACME_BIN）
	Home    string // 工具工作目录（ACME_HOME，state 集中面板侧）
	Webroot string // http-01 webroot（FERRY_ACME_WEBROOT）
	CA      string // ACME 服务器（缺省 letsencrypt）
	Run     Runner
	// ReadExpiry 读域名证书到期（缺省 openssl x509），测试可注入。
	ReadExpiry func(domain string) (time.Time, error)
}

func (m *Manager) runner() Runner {
	if m.Run != nil {
		return m.Run
	}
	return m.realRun
}

// realRun 受控执行工具命令：工作目录 Home、附加 env 拼进环境，输出合并返回。
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

// dnsPlugin 把 DNS 商类型映射到 acme.sh 的 dns API 插件名（横扩一家登记一条）。
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

// Issue 首签/重签：domains[0] 为主域名，其余逐个 -d；env 为凭证通道
// （dns-01 必带，http-01 为空）。返回合并输出（作任务留痕尾行）。
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

// Renew 续期（acme.sh 自判未到续期窗口时是幂等空跑）。
func (m *Manager) Renew(ctx context.Context, domain string) (string, error) {
	if domain == "" {
		return "", errors.New("cert: domain is required")
	}
	return m.runner()(ctx, []string{"--renew", "-d", domain}, nil)
}

// Expiry 读域名证书到期：缺省 openssl 解 acme.sh 产物目录的 fullchain
// （<域名>_ecc/fullchain.cer）；读不出返回错误，调用方保留原值不阻塞。
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
