// cloud-init bootstrap (OS-3): the instance created by apply gets the pre-issued
// agent config and systemd unit injected via user_data, so ferry-agent is installed at boot
// and registers with the panel on first contact (hello validates the node token, no new
// registration protocol needed). Keeps the same conventions as deploy/agent-install.sh:
// config path /etc/ferry/agent.json, install path /usr/local/bin/ferry-agent,
package provision

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// AgentConfigFile is the config written to /etc/ferry/agent.json on the node (used for the agent's outbound connection).
type AgentConfigFile struct {
	PanelURL string `json:"panel_url"` // ws(s)://host/agent/ws
	AgentID  string `json:"agent_id"`
	Token    string `json:"token"` // pre-issued node token
}

// AgentConfigJSON renders the node config JSON (base64-encoded into cloud-init, never plaintext in HCL).
func AgentConfigJSON(cfg AgentConfigFile) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PanelWSURL converts the panel base URL to the agent ws address: http->ws, https->wss, appending /agent/ws.
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

// agentSystemdUnit matches agent-install.sh: restart automatically on disconnect.
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

// agentInstallScript downloads and installs ferry-agent and starts the service, branching on uname -m;
// verifies the accompanying .sha256 when present (same convention as agent-install.sh), skips without blocking when absent.
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

// CloudInit renders the #cloud-config user_data: the agent config and systemd unit are
// pre-provisioned (write_files, before runcmd), and runcmd downloads, installs and starts. The cloud-init
// document is string concatenation rather than YAML serialization: all content comes from this package, and
// escaping is confined to agent.json (where the token lives) via base64.
func CloudInit(configJSON, agentBase, version string) string {
	cfgB64 := base64.StdEncoding.EncodeToString([]byte(configJSON))
	script := strings.NewReplacer("__BASE__", strings.TrimRight(agentBase, "/"), "__VER__", version).
		Replace(agentInstallScript)
	// Indent the block scalar as a whole: the prefix carries the first line, the rest is padded at each newline.
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
