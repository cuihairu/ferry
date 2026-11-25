#!/usr/bin/env bash
# ferry-agent node-side one-shot install (A-24):
#   download the binary (or --binary for a local file) -> sha256 verify -> write /etc/ferry/agent.json
#   -> install the systemd service and start it.
#
# Usage (run as root on the node):
#   agent-install.sh --panel https://panel.example.com --token <node token>
#
# Common optional arguments:
#   --agent-id NAME     node identifier (defaults to the hostname)
#   --version V         version to install (default latest = the newest stable GitHub release, resolved to the real
#                       version via the API; with no stable release it errors out and never fabricates a vlatest URL)
#   --base URL          download base (default GitHub Releases; an intranet file server can be used)
#   --binary PATH       install from a local binary (skips download, offline scenarios)
#   --sha256 HEX        manual checksum (when there is no companion .sha256 file)
#   --ca/--cert/--key   mTLS triple paths; when given, written into agent.json's tls section
#   --bw-up/--bw-down M node bandwidth Mbps (required by agent config validation; default 100, editable on the panel's node page)
#   --node-role R     node cluster role entry/landing/both (when omitted the agent defaults to landing)
#   FERRY_SYSTEMD_UNIT_DIR  unit directory override (default /etc/systemd/system; for tests/non-standard environments)
#
# relay role (E-5/E-33; with --role relay the host starts the relay data plane, dialing the landing through a transport plugin):
#   --role relay        systemd starts with -role relay (probe-role config lives in the probes file section, not installed here)
#   --relay-listen ADDR local listen address (default 127.0.0.1:1080)
#   --relay-landing A   landing address host:port (required for role=relay)
#   --relay-tunnel NAME transport plugin (tls-camo/quic/ws-tls/ssh; empty = default tls-camo)
#   --relay-server-name TLS camouflage domain; with tunnel=ssh it is the landing sshd address (required)
#   --relay-ca FILE     private CA (verifies the landing certificate; empty = system roots)
#   --relay-ssh-user U    ssh only: login user (publickey)
#   --relay-ssh-key F     ssh only: local private key path (required together with user/forward-addr)
#   --relay-ssh-known-hosts F  ssh only: known_hosts path (empty = skip host key verification, bootstrap phase only)
#   --relay-ssh-forward-addr A ssh only: the direct-tcpip target (the landing's local relay listener)
#
# QUIC sidecar (E-18b; --quic additionally installs the standalone ferry-quic binary and ferry-quic.service):
#   --quic              also install the ferry-quic sidecar (needed for the quic transport; not installed without QUIC)
#   --quic-install P    sidecar on-disk path (default /usr/local/bin/ferry-quic)
#   --quic-listen ADDR  sidecar bridge listen address (default 127.0.0.1:7300; a non-default value is written into the agent's env)
#   --quic-ca FILE      CA the sidecar uses to verify the peer's QUIC certificate (empty = system roots)
#   --quic-insecure     skip QUIC certificate verification (bootstrap debugging only)
#   --quic-binary PATH  local ferry-quic binary (offline scenarios)
#
# No node token yet? First issue a one-time enrollment credential on the panel host (FERRY-001, valid 60 minutes, single use):
#   curl -X POST https://panel.example.com/api/enrollment-tokens \
#     -H "Authorization: Bearer <admin JWT or API token>" -d '{"note":"hk1 first install"}'
# Then on the node use --register + --enroll-token to exchange it for the node token and install:
#   agent-install.sh --panel https://panel.example.com --register hk1 \
#     --enroll-token <enrollment credential> --address hk1.example.com --port 443 --protocol vless
set -euo pipefail

PANEL="" TOKEN="" REGISTER="" ENROLL_TOKEN="" ADDRESS="" PORT=443 PROTOCOL=vless
AGENT_ID="$(hostname)" VERSION="latest"
BASE_DEFAULT="https://github.com/cuihairu/ferry/releases/download"
BASE="$BASE_DEFAULT"
BINARY="" SHA256="" CA="" CERT="" KEY="" BW_UP=100 BW_DOWN=100 NODE_ROLE=""
CONFIG=/etc/ferry/agent.json INSTALL=/usr/local/bin/ferry-agent SERVICE=ferry-agent
ROLE="" RELAY_LISTEN=127.0.0.1:1080 RELAY_LANDING="" RELAY_TUNNEL="" RELAY_SNI="" RELAY_CA=""
RELAY_SSH_USER="" RELAY_SSH_KEY="" RELAY_SSH_KNOWN_HOSTS="" RELAY_SSH_FORWARD=""
QUIC=0 QUIC_LISTEN=127.0.0.1:7300 QUIC_CA="" QUIC_INSECURE="" QUIC_BINARY="" QUIC_INSTALL=""

die() { echo "agent-install: $*" >&2; exit 1; }
log() { echo "[install] $*"; }

# wait_service_active waits after restart until the service is truly active (at most ~10 seconds): a restart
# returning does not mean ready — the agent still has to load its config and connect; polling avoids misjudging
# "still starting" as a failure.
# The check requires both ActiveState=active and Result=success: is-active alone would misjudge a crash-looping
# unit as a successful install — under Restart=always an instantly-exiting process is relaunched repeatedly, the
# unit is in activating almost the entire time between restarts, yet a transient active window exists between fork
# and the exec failure being recorded (over 1 second on slow hosts), so two consecutive is-active checks can both
# land in it; Result records the most recent start/stop outcome, and an exec failure means exit-code that is not
# cleared by automatic restarts — only a genuine successful start brings it back to success, regardless of sampling timing.
wait_service_active() {
  local svc="$1" i
  for i in 1 2 3 4 5; do
    if [ "$(systemctl show -p ActiveState --value "$svc" 2>/dev/null)" = active ] \
      && [ "$(systemctl show -p Result --value "$svc" 2>/dev/null)" = success ]; then
      sleep 1
      [ "$(systemctl show -p ActiveState --value "$svc" 2>/dev/null)" = active ] \
        && [ "$(systemctl show -p Result --value "$svc" 2>/dev/null)" = success ] && return 0
    fi
    sleep 1
  done
  [ "$(systemctl show -p ActiveState --value "$svc" 2>/dev/null)" = active ] \
    && [ "$(systemctl show -p Result --value "$svc" 2>/dev/null)" = success ]
}

# dump_service_logs failure diagnostics (FERRY-005): prints the unit's recent log before the rollback runs, so the
# upgrade-failure scene is visible immediately instead of guess-then-check-journalctl.
dump_service_logs() {
  echo "--- $1 最近日志（失败诊断） ---" >&2
  journalctl -u "$1" -n 20 --no-pager >&2 2>/dev/null || true
}

# json_field reads a field value from a JSON file: jq/python3 first, falling back to text extraction only when
# neither exists (FERRY-001: parsing the registration response must no longer depend on naive text extraction,
# which is sensitive to field formatting).
json_field() { # $1=field name $2=file
  local f="$1" file="$2" v=""
  if command -v jq >/dev/null 2>&1; then
    v=$(jq -r --arg k "$f" '.[$k] // empty' "$file" 2>/dev/null) || v=""
  elif command -v python3 >/dev/null 2>&1; then
    v=$(python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception:
    pass' "$f" "$file" 2>/dev/null) || v=""
  else
    v=$(grep -o "\"$f\":\"[^\"]*\"" "$file" 2>/dev/null | head -1 | cut -d'"' -f4)
  fi
  printf '%s' "$v"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --panel)    PANEL="$2"; shift 2 ;;
    --token)    TOKEN="$2"; shift 2 ;;
    --register) REGISTER="$2"; shift 2 ;;
    --enroll-token) ENROLL_TOKEN="$2"; shift 2 ;;
    --address)  ADDRESS="$2"; shift 2 ;;
    --port)     PORT="$2"; shift 2 ;;
    --protocol) PROTOCOL="$2"; shift 2 ;;
    --agent-id) AGENT_ID="$2"; shift 2 ;;
    --version)  VERSION="$2"; shift 2 ;;
    --base)     BASE="${2%/}"; shift 2 ;;
    --binary)   BINARY="$2"; shift 2 ;;
    --sha256)   SHA256="$2"; shift 2 ;;
    --ca)       CA="$2"; shift 2 ;;
    --cert)     CERT="$2"; shift 2 ;;
    --key)      KEY="$2"; shift 2 ;;
    --bw-up)    BW_UP="$2"; shift 2 ;;
    --bw-down)  BW_DOWN="$2"; shift 2 ;;
    --node-role) NODE_ROLE="$2"; shift 2 ;;
    --config)   CONFIG="$2"; shift 2 ;;
    --install)  INSTALL="$2"; shift 2 ;;
    --role)     ROLE="$2"; shift 2 ;;
    --relay-listen)  RELAY_LISTEN="$2"; shift 2 ;;
    --relay-landing) RELAY_LANDING="$2"; shift 2 ;;
    --relay-tunnel)  RELAY_TUNNEL="$2"; shift 2 ;;
    --relay-server-name) RELAY_SNI="$2"; shift 2 ;;
    --relay-ca)      RELAY_CA="$2"; shift 2 ;;
    --relay-ssh-user)   RELAY_SSH_USER="$2"; shift 2 ;;
    --relay-ssh-key)    RELAY_SSH_KEY="$2"; shift 2 ;;
    --relay-ssh-known-hosts) RELAY_SSH_KNOWN_HOSTS="$2"; shift 2 ;;
    --relay-ssh-forward-addr) RELAY_SSH_FORWARD="$2"; shift 2 ;;
    --quic)     QUIC=1; shift ;;
    --quic-install) QUIC_INSTALL="$2"; shift 2 ;;
    --quic-listen)  QUIC_LISTEN="$2"; shift 2 ;;
    --quic-ca)      QUIC_CA="$2"; shift 2 ;;
    --quic-insecure) QUIC_INSECURE=1; shift ;;
    --quic-binary)  QUIC_BINARY="$2"; shift 2 ;;
    -h|--help)  sed -n '2,46p' "$0"; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || die "请用 root 执行"
[ -n "$PANEL" ] || die "--panel 必填（面板基址，如 https://panel.example.com）"
[ -n "$TOKEN" ] || [ -n "$REGISTER" ] || die "--token 与 --register 二选一"
[ -z "$REGISTER" ] || [ -n "$ENROLL_TOKEN" ] || die "--register 需同时给 --enroll-token（一次性注册凭据：管理员侧先签发，见脚本头注释）"
[ -z "$REGISTER" ] || [ -n "$ADDRESS" ] || die "--register 需要同时给 --address"
case "$BW_UP$BW_DOWN" in *[!0-9]*) die "--bw-up/--bw-down 需为正整数 Mbps" ;; esac
[ "$BW_UP" -gt 0 ] && [ "$BW_DOWN" -gt 0 ] || die "--bw-up/--bw-down 需为正整数 Mbps（agent 配置校验必填）"
[ -z "$NODE_ROLE" ] || case "$NODE_ROLE" in entry|landing|both) ;; *) die "--node-role 仅支持 entry/landing/both" ;; esac
[ -z "$ROLE" ] || [ "$ROLE" = "relay" ] || die "--role 仅支持 relay（probe 的探测任务以配置文件 probes 段为准）"
if [ "$ROLE" = "relay" ]; then
  [ -n "$RELAY_LANDING" ] || die "--role relay 需要同时给 --relay-landing（落地地址 host:port）"
  case "$RELAY_TUNNEL" in ""|tls-camo|quic|ws-tls|ssh) ;; *) die "--relay-tunnel 仅支持 tls-camo/quic/ws-tls/ssh" ;; esac
  if [ "$RELAY_TUNNEL" = "ssh" ]; then
    [ -n "$RELAY_SNI" ] || die "tunnel=ssh 需 --relay-server-name 指落地 sshd 地址"
    [ -n "$RELAY_SSH_USER" ] && [ -n "$RELAY_SSH_KEY" ] && [ -n "$RELAY_SSH_FORWARD" ] \
      || die "tunnel=ssh 需 --relay-ssh-user/--relay-ssh-key/--relay-ssh-forward-addr 齐备（publickey 单一认证）"
    [ -f "$RELAY_SSH_KEY" ] || die "私钥文件不存在：$RELAY_SSH_KEY"
  fi
  [ -z "$RELAY_CA" ] || [ -f "$RELAY_CA" ] || die "CA 文件不存在：$RELAY_CA"
fi
if [ "$QUIC" = 1 ] && [ -n "$QUIC_BINARY" ]; then
  [ -f "$QUIC_BINARY" ] || die "本地 ferry-quic 不存在：$QUIC_BINARY"
fi
: "${QUIC_INSTALL:=/usr/local/bin/ferry-quic}" # set early: the EXIT trap also references it

# ---- panel_url: http->ws, https->wss, always appending /agent/ws ----
case "$PANEL" in
  http://*)  WS="ws://${PANEL#http://}" ;;
  https://*) WS="wss://${PANEL#https://}" ;;
  ws://*|wss://*) WS="$PANEL" ;;
  *) die "--panel 需以 http(s):// 开头" ;;
esac
PANEL_URL="${WS%/}/agent/ws"

# ---- Fetch the binary: a local file or a versioned download ----
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" "$INSTALL.new" "$CONFIG.new" "$QUIC_INSTALL.new"' EXIT

# Version resolution (FERRY-002): before building the download URL, latest is resolved through the GitHub API to
# the actual newest stable version; if it cannot resolve (no official release yet) it fails immediately and never
# fabricates a vlatest URL; an explicit version is used as-is with no silent fallback. A custom --base source
# cannot resolve latest, so an explicit version is required.
need_dl=0
if [ -z "$BINARY" ] || { [ "$QUIC" = 1 ] && [ -z "$QUIC_BINARY" ]; }; then need_dl=1; fi
if [ "$need_dl" = 1 ] && [ "$VERSION" = "latest" ]; then
  [ "$BASE" = "$BASE_DEFAULT" ] || die "--version latest 仅支持默认 GitHub Releases 基址；--base 自建源请显式给 --version"
  log "解析 latest 稳定版本 ..."
  code=$(curl -sS -o "$tmp/latest.json" -w '%{http_code}' \
    "https://api.github.com/repos/cuihairu/ferry/releases/latest") \
    || die "查询最新稳定版失败（GitHub API 不可达）：请显式指定 --version"
  [ "$code" = 200 ] || die "尚无正式稳定版发布（HTTP $code）：夜间构建是 CI 产物不落地 Releases，请显式指定 --version"
  tag=$(json_field tag_name "$tmp/latest.json")
  case "$tag" in v*) VERSION="${tag#v}" ;; *) die "最新版 tag 解析失败：请显式指定 --version" ;; esac
  log "latest → v$VERSION"
fi

if [ -n "$BINARY" ]; then
  [ -f "$BINARY" ] || die "本地二进制不存在：$BINARY"
  cp "$BINARY" "$tmp/ferry-agent"
else
  case "$(uname -m)" in
    x86_64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "不支持的架构：$(uname -m)（暂支持 amd64/arm64）" ;;
  esac
  URL="$BASE/v$VERSION/ferry-agent_${VERSION}_linux_${ARCH}.tar.gz"
  log "下载 $URL ..."
  curl -fsSL "$URL" -o "$tmp/agent.tar.gz" || die "下载失败"
  # Verification: prefer the release's companion .sha256, then a manual --sha256 value
  SUM_URL="$BASE/v$VERSION/ferry-agent_${VERSION}_linux_${ARCH}.tar.gz.sha256"
  if curl -fsSL "$SUM_URL" -o "$tmp/agent.sha256" 2>/dev/null; then
    # The .sha256 records the release artifact's original long filename while the local download is named agent.tar.gz: rewrite the comparison name before verifying
    awk '{print $1"  agent.tar.gz"}' "$tmp/agent.sha256" > "$tmp/agent.checksum"
    (cd "$tmp" && sha256sum -c agent.checksum) || die "sha256 校验失败"
  elif [ -n "$SHA256" ]; then
    echo "$SHA256  $tmp/agent.tar.gz" | sha256sum -c - || die "sha256 校验失败"
  else
    log "警告：无 .sha256 且未指定 --sha256，跳过校验"
  fi
  tar -xzf "$tmp/agent.tar.gz" -C "$tmp"
  [ -f "$tmp/ferry-agent" ] || die "压缩包里没有 ferry-agent"
fi

# ---- QUIC sidecar (E-18b): an optional release artifact; not installed unless the QUIC transport is used ----
# (this only downloads + verifies + unpacks; on-disk installation happens uniformly after registration succeeds)
if [ "$QUIC" = 1 ]; then
  if [ -n "$QUIC_BINARY" ]; then
    cp "$QUIC_BINARY" "$tmp/ferry-quic"
  else
    [ -n "${ARCH:-}" ] || case "$(uname -m)" in
      x86_64)  ARCH=amd64 ;;
      aarch64|arm64) ARCH=arm64 ;;
      *) die "不支持的架构：$(uname -m)" ;;
    esac
    QUIC_URL="$BASE/v$VERSION/ferry-quic_${VERSION}_linux_${ARCH}.tar.gz"
    log "下载 $QUIC_URL ..."
    curl -fsSL "$QUIC_URL" -o "$tmp/quic.tar.gz" || die "ferry-quic 下载失败（--quic-binary 可离线装）"
    QUIC_SUM_URL="$BASE/v$VERSION/ferry-quic_${VERSION}_linux_${ARCH}.tar.gz.sha256"
    if curl -fsSL "$QUIC_SUM_URL" -o "$tmp/quic.sha256" 2>/dev/null; then
      awk '{print $1"  quic.tar.gz"}' "$tmp/quic.sha256" > "$tmp/quic.checksum"
      (cd "$tmp" && sha256sum -c quic.checksum) || die "ferry-quic sha256 校验失败"
    else
      log "警告：ferry-quic 无伴随 .sha256，跳过校验"
    fi
    tar -xzf "$tmp/quic.tar.gz" -C "$tmp"
    [ -f "$tmp/ferry-quic" ] || die "压缩包里没有 ferry-quic"
  fi
fi

# ---- Registration (FERRY-001): the node is created only after every asset passes download and verification, so a
# download/verification failure never leaves an orphan node record; the credential travels only in the
# Authorization header — never in logs, never on disk.
if [ -z "$TOKEN" ]; then
  log "向面板注册节点 $REGISTER ..."
  code=$(curl -sS -o "$tmp/enroll.json" -w '%{http_code}' -X POST "$PANEL/api/enroll" \
    -H "Authorization: Bearer $ENROLL_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$REGISTER\",\"address\":\"$ADDRESS\",\"port\":$PORT,\"protocol\":\"$PROTOCOL\"}") \
    || die "注册请求失败：检查面板地址与网络"
  [ "$code" = 201 ] || die "注册被拒（HTTP $code）：注册凭据无效/过期/已用或载荷不合规，请在面板侧重新签发后再试"
  TOKEN=$(json_field token "$tmp/enroll.json")
  [ -n "$TOKEN" ] || die "注册响应里没有 token 字段"
  log "节点已创建，令牌已取得（面板节点页可查）"
fi

# ---- Install (FERRY-005): back up the active files (.prev) first, then replace atomically (same-directory rename,
# never a half-written binary); a failed start rolls back automatically to the pre-change state, and success clears the backups. ----
unit_dir=${FERRY_SYSTEMD_UNIT_DIR:-/etc/systemd/system}
unit="$unit_dir/$SERVICE.service"
bak_install="$INSTALL.prev"; bak_unit="$unit.prev"
had_prev=0
if [ -f "$INSTALL" ]; then cp -a "$INSTALL" "$bak_install"; had_prev=1; fi
if [ -f "$unit" ]; then cp -a "$unit" "$bak_unit"; fi
install -m 0755 "$tmp/ferry-agent" "$INSTALL.new"
mv -f "$INSTALL.new" "$INSTALL"
if [ "$QUIC" = 1 ]; then
  bak_quic="$QUIC_INSTALL.prev"
  bak_quic_unit="$unit_dir/ferry-quic.service.prev"
  had_prev_quic=0
  if [ -f "$QUIC_INSTALL" ]; then
    cp -a "$QUIC_INSTALL" "$bak_quic"
    if [ -f "$unit_dir/ferry-quic.service" ]; then
      cp -a "$unit_dir/ferry-quic.service" "$bak_quic_unit"
    fi
    had_prev_quic=1
  fi
  install -m 0755 "$tmp/ferry-quic" "$QUIC_INSTALL.new"
  mv -f "$QUIC_INSTALL.new" "$QUIC_INSTALL"
fi

# ---- Config: $CONFIG (kept when it already exists, never overwriting on-site config; fresh writes go through a
# temp file + rename, so an interruption never leaves a half-written config) ----
mkdir -p "$(dirname "$CONFIG")"
if [ -f "$CONFIG" ]; then
  log "配置已存在，保留：$CONFIG（如需重写请先删除）"
else
  {
    echo '{'
    echo "  \"panel_url\": \"$PANEL_URL\","
    echo "  \"agent_id\": \"$AGENT_ID\","
    echo "  \"token\": \"$TOKEN\""
    # The meta bandwidth is required by the agent's config validation (the default-less config would fail to start); the
    # rest of the metadata is filled in agent-side by applyMetaDefaults, and the FERRY_NODE_* env vars and the panel can adjust it later.
    echo ',  "meta": {'
    [ -n "$NODE_ROLE" ] && echo "    \"role\": \"$NODE_ROLE\","
    echo "    \"bw_up_mbps\": $BW_UP,"
    echo "    \"bw_down_mbps\": $BW_DOWN"
    echo '  }'
    if [ -n "$CA" ] || [ -n "$CERT" ] || [ -n "$KEY" ]; then
      [ -n "$CA" ] && [ -n "$CERT" ] && [ -n "$KEY" ] || die "mTLS 需 --ca/--cert/--key 三者齐备"
      for f in "$CA" "$CERT" "$KEY"; do [ -f "$f" ] || die "证书文件不存在：$f"; done
      echo ',  "tls": {'
      echo "    \"ca_file\": \"$CA\","
      echo "    \"cert_file\": \"$CERT\","
      echo "    \"key_file\": \"$KEY\""
      echo '  }'
    fi
    if [ "$ROLE" = "relay" ]; then
      echo ',  "relay": {'
      echo "    \"listen\": \"$RELAY_LISTEN\","
      echo "    \"landing_addr\": \"$RELAY_LANDING\""
      [ -n "$RELAY_TUNNEL" ] && echo "    ,\"tunnel\": \"$RELAY_TUNNEL\""
      [ -n "$RELAY_SNI" ] && echo "    ,\"server_name\": \"$RELAY_SNI\""
      [ -n "$RELAY_CA" ] && echo "    ,\"ca_file\": \"$RELAY_CA\""
      [ -n "$RELAY_SSH_USER" ] && echo "    ,\"ssh_user\": \"$RELAY_SSH_USER\""
      [ -n "$RELAY_SSH_KEY" ] && echo "    ,\"ssh_key_file\": \"$RELAY_SSH_KEY\""
      [ -n "$RELAY_SSH_KNOWN_HOSTS" ] && echo "    ,\"ssh_known_hosts\": \"$RELAY_SSH_KNOWN_HOSTS\""
      [ -n "$RELAY_SSH_FORWARD" ] && echo "    ,\"ssh_forward_addr\": \"$RELAY_SSH_FORWARD\""
      echo '  }'
    fi
    echo '}'
  } > "$CONFIG.new"
  mv -f "$CONFIG.new" "$CONFIG"
  chmod 600 "$CONFIG"
  log "已写配置 $CONFIG"
fi

# ---- systemd: the agent's main service (role=relay appends -role relay) ----
ROLE_ARGS=""
[ "$ROLE" = "relay" ] && ROLE_ARGS=" -role relay"
# The quic plugin dials the sidecar bridge: the env var is written only for a non-default listen (both ends default to 127.0.0.1:7300)
QUIC_ENV=""
if [ "$QUIC" = 1 ] && [ "$QUIC_LISTEN" != "127.0.0.1:7300" ]; then
  QUIC_ENV="Environment=FERRY_QUIC_SIDECAR=$QUIC_LISTEN"
fi
cat > "$unit.new" <<EOF
[Unit]
Description=ferry agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$INSTALL -config $CONFIG$ROLE_ARGS
$QUIC_ENV
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
mv -f "$unit.new" "$unit"

# Rollback (FERRY-005): with a previous binary = restore the prior binary and unit, then restart (the node never stays
# in a broken state because of one failed upgrade); first install = disable and clean up this run's artifacts. The .prev
# backups are kept for manual inspection.
rollback_agent() {
  log "启动失败，回滚到变更前状态 ..."
  if [ "$had_prev" = 1 ]; then
    cp -a "$bak_install" "$INSTALL"
    if [ -f "$bak_unit" ]; then cp -a "$bak_unit" "$unit"; fi
    systemctl daemon-reload >/dev/null 2>&1 || true
    systemctl restart "$SERVICE" >/dev/null 2>&1 || true
    log "已还原上一版本（备份保留在 $bak_install / $bak_unit，供手动核对）"
  else
    systemctl stop "$SERVICE" >/dev/null 2>&1 || true
    systemctl disable "$SERVICE" >/dev/null 2>&1 || true
    rm -f "$unit" "$INSTALL"
    systemctl daemon-reload >/dev/null 2>&1 || true
    log "首装失败，已清理本次安装产物"
  fi
}
if systemctl daemon-reload \
   && systemctl enable "$SERVICE" >/dev/null \
   && systemctl restart "$SERVICE" \
   && wait_service_active "$SERVICE"; then
  rm -f "$bak_install" "$bak_unit"
else
  dump_service_logs "$SERVICE"
  rollback_agent
  die "ferry-agent 启动失败，已回滚：按上方诊断排查（journalctl -u $SERVICE）后重试"
fi
systemctl --no-pager --lines=5 status "$SERVICE" || true
log "完成：journalctl -u $SERVICE -f 查看连接日志"

# ---- systemd: the ferry-quic sidecar (client mode, a local bridge dialing the landing over QUIC) ----
if [ "$QUIC" = 1 ]; then
  QUIC_ARGS="client --listen $QUIC_LISTEN"
  [ -n "$QUIC_CA" ] && QUIC_ARGS="$QUIC_ARGS --ca $QUIC_CA"
  [ "$QUIC_INSECURE" = 1 ] && QUIC_ARGS="$QUIC_ARGS --insecure"
  quic_unit="$unit_dir/ferry-quic.service"
  cat > "$quic_unit.new" <<EOF
[Unit]
Description=ferry-quic sidecar (QUIC transport bridge, E-18b)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$QUIC_INSTALL $QUIC_ARGS
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  mv -f "$quic_unit.new" "$quic_unit"

  # The sidecar's rollback is independent of the agent: restore the old sidecar or clean this run's artifacts; the agent is unaffected.
  rollback_quic() {
    log "ferry-quic 启动失败，回滚 sidecar ..."
    if [ "$had_prev_quic" = 1 ]; then
      cp -a "$bak_quic" "$QUIC_INSTALL"
      if [ -f "$bak_quic_unit" ]; then cp -a "$bak_quic_unit" "$quic_unit"; fi
      systemctl daemon-reload >/dev/null 2>&1 || true
      systemctl restart ferry-quic >/dev/null 2>&1 || true
      log "sidecar 已还原上一版本（备份保留在 $bak_quic / $bak_quic_unit）"
    else
      systemctl stop ferry-quic >/dev/null 2>&1 || true
      systemctl disable ferry-quic >/dev/null 2>&1 || true
      rm -f "$quic_unit" "$QUIC_INSTALL"
      systemctl daemon-reload >/dev/null 2>&1 || true
      log "sidecar 首装失败，已清理本次产物"
    fi
  }
  if systemctl daemon-reload \
     && systemctl enable ferry-quic >/dev/null \
     && systemctl restart ferry-quic \
     && wait_service_active ferry-quic; then
    [ "$had_prev_quic" = 1 ] && rm -f "$bak_quic" "$bak_quic_unit"
  else
    dump_service_logs ferry-quic
    rollback_quic
    die "ferry-quic 启动失败，已回滚 sidecar（agent 不受影响）：按上方诊断排查后重试"
  fi
  systemctl --no-pager --lines=5 status ferry-quic || true
  log "完成：journalctl -u ferry-quic -f 查看 sidecar 日志"
fi
