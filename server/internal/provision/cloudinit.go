// cloud-init 初始化（OS-3）：apply 创建的实例经 user_data 注入预签发的
// agent 配置与 systemd 服务，开机自动安装 ferry-agent 并首连面板注册
// （hello 校验节点 token，无需新注册协议）。口径与 deploy/agent-install.sh
// 保持一致：配置路径 /etc/ferry/agent.json、安装路径 /usr/local/bin/ferry-agent、
// 下载地址 $BASE/v$VER/ferry-agent_${VER}_linux_${ARCH}.tar.gz（amd64/arm64）。
package provision

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// AgentConfigFile 是写入节点 /etc/ferry/agent.json 的配置（agent 出站连接用）。
type AgentConfigFile struct {
	PanelURL string `json:"panel_url"` // ws(s)://host/agent/ws
	AgentID  string `json:"agent_id"`
	Token    string `json:"token"` // 预签发节点令牌
}

// AgentConfigJSON 渲染节点配置 JSON（经 base64 进 cloud-init，不落明文于 HCL）。
func AgentConfigJSON(cfg AgentConfigFile) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PanelWSURL 把面板基址转成 agent ws 地址：http→ws、https→wss，统一拼 /agent/ws。
func PanelWSURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	var ws string
	switch {
	case strings.HasPrefix(base, "https://"):
		ws = "wss://" + base[len("https://"):]
	case strings.HasPrefix(base, "http://"):
		ws = "ws://" + base[len("http://"):]
	case strings.HasPrefix(base, "wss://"), strings.HasPrefix(base, "ws://"):
		ws = base
	default:
		ws = "ws://" + base
	}
	return ws + "/agent/ws"
}

// agentSystemdUnit 与 agent-install.sh 同口径：断线自动拉起。
const agentSystemdUnit = `[Unit]
Description=ferry agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/ferry-agent -config /etc/ferry/agent.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// agentInstallScript 按 uname -m 分支下载安装 ferry-agent 并启动服务；
// 伴随 .sha256 有则校验（与 agent-install.sh 同口径），无则跳过不阻断。
const agentInstallScript = `set -eu
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "ferry-agent: unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
url="__BASE__/v__VER__/ferry-agent___VER___linux_${arch}.tar.gz"
tmp=$(mktemp -d)
curl -fsSL "$url" -o "$tmp/agent.tar.gz"
if curl -fsSL "$url.sha256" -o "$tmp/agent.tar.gz.sha256" 2>/dev/null; then
  (cd "$tmp" && sha256sum -c agent.tar.gz.sha256)
fi
tar -xzf "$tmp/agent.tar.gz" -C "$tmp"
install -m 0755 "$tmp/ferry-agent" /usr/local/bin/ferry-agent
rm -rf "$tmp"
systemctl daemon-reload
systemctl enable ferry-agent >/dev/null
systemctl restart ferry-agent
`

// CloudInit 渲染 #cloud-config user_data：预置 agent 配置与 systemd unit
// （write_files，先于 runcmd），runcmd 下载安装并启动。cloud-init 字符串
// 拼接而非 YAML 库序列化：内容全部由本包产出，转义点收敛在 base64 的
// agent.json（token 所在处）。
func CloudInit(configJSON, agentBase, version string) string {
	cfgB64 := base64.StdEncoding.EncodeToString([]byte(configJSON))
	script := strings.NewReplacer("__BASE__", strings.TrimRight(agentBase, "/"), "__VER__", version).
		Replace(agentInstallScript)
	// 块标量整体缩进：除首行由前缀带出，其余行在换行处补齐空格。
	unit := strings.ReplaceAll(strings.TrimSuffix(agentSystemdUnit, "\n"), "\n", "\n      ")
	body := strings.ReplaceAll(strings.TrimSuffix(script, "\n"), "\n", "\n    ")
	return "#cloud-config\n" +
		"write_files:\n" +
		"  - path: /etc/ferry/agent.json\n" +
		"    permissions: \"0600\"\n" +
		"    encoding: b64\n" +
		"    content: " + cfgB64 + "\n" +
		"  - path: /etc/systemd/system/ferry-agent.service\n" +
		"    content: |\n" +
		"      " + unit + "\n" +
		"runcmd:\n" +
		"  - |\n" +
		"    " + body
}
