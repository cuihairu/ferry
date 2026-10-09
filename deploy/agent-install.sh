#!/usr/bin/env bash
# ferry-agent 节点侧一键安装（A-24）：
#   下载二进制（或 --binary 用本地文件）→ sha256 校验 → 写 /etc/ferry/agent.json
#   → 装 systemd 服务并启动。
#
# 用法（节点机 root 执行）：
#   agent-install.sh --panel https://panel.example.com --token <节点令牌>
#
# 常用可选参数：
#   --agent-id NAME     节点标识（默认主机名）
#   --version V         安装版本（默认 latest，对应 release tag vV）
#   --base URL          下载基址（默认 GitHub Releases，内网可指到自建文件服务）
#   --binary PATH       用本地二进制安装（跳过下载，离线场景）
#   --sha256 HEX        手动指定校验值（无伴随 .sha256 文件时）
#   --ca/--cert/--key   mTLS 三件套路径，给了就写进 agent.json 的 tls 段
#   --bw-up/--bw-down M 节点带宽 Mbps（agent 配置校验必填项，默认 100，面板节点页可改）
#   --node-role R     节点集群角色 entry/landing/both（不写时 agent 侧默认 landing）
#
# relay 角色（E-5/E-33，--role relay 时本机起 relay 数据面，经传输插件拨落地）：
#   --role relay        systemd 以 -role relay 启动（probe 角色配置以 probes 文件段为准，不在此装）
#   --relay-listen ADDR 本机监听（默认 127.0.0.1:1080）
#   --relay-landing A   落地地址 host:port（role=relay 必填）
#   --relay-tunnel NAME 传输插件（tls-camo/quic/ws-tls/ssh，空=默认 tls-camo）
#   --relay-server-name TLS 伪装域名；tunnel=ssh 时为落地 sshd 地址（必填）
#   --relay-ca FILE     私有 CA（校验落地证书，空=系统根）
#   --relay-ssh-user U    ssh 专用：登录用户（publickey）
#   --relay-ssh-key F     ssh 专用：本机私钥路径（与 user/forward-addr 一起必填）
#   --relay-ssh-known-hosts F  ssh 专用：known_hosts 路径（空=跳过 host key 校验，仅引导期）
#   --relay-ssh-forward-addr A ssh 专用：direct-tcpip 目标（落地本机 relay 监听）
#
# QUIC sidecar（E-18b，--quic 额外装 ferry-quic 独立二进制与 ferry-quic.service）：
#   --quic              同时安装 ferry-quic sidecar（quic 传输需要，不用 QUIC 不装）
#   --quic-listen ADDR  sidecar 桥监听（默认 127.0.0.1:7300，非默认时写进 agent 环境变量）
#   --quic-ca FILE      sidecar 校验对端 QUIC 证书的 CA（空=系统根）
#   --quic-insecure     跳过 QUIC 证书校验（仅引导调试）
#   --quic-binary PATH  本地 ferry-quic 二进制（离线场景）
#
# 令牌还没有？在面板机上用 --register 顺手建节点拿令牌：
#   agent-install.sh --panel https://panel.example.com --register hk1 \
#     --address hk1.example.com --port 443 --protocol vless
set -euo pipefail

PANEL="" TOKEN="" REGISTER="" ADDRESS="" PORT=443 PROTOCOL=vless
AGENT_ID="$(hostname)" VERSION="latest"
BASE="https://github.com/cuihairu/ferry/releases/download"
BINARY="" SHA256="" CA="" CERT="" KEY="" BW_UP=100 BW_DOWN=100 NODE_ROLE=""
CONFIG=/etc/ferry/agent.json INSTALL=/usr/local/bin/ferry-agent SERVICE=ferry-agent
ROLE="" RELAY_LISTEN=127.0.0.1:1080 RELAY_LANDING="" RELAY_TUNNEL="" RELAY_SNI="" RELAY_CA=""
RELAY_SSH_USER="" RELAY_SSH_KEY="" RELAY_SSH_KNOWN_HOSTS="" RELAY_SSH_FORWARD=""
QUIC=0 QUIC_LISTEN=127.0.0.1:7300 QUIC_CA="" QUIC_INSECURE="" QUIC_BINARY=""

die() { echo "agent-install: $*" >&2; exit 1; }
log() { echo "[install] $*"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --panel)    PANEL="$2"; shift 2 ;;
    --token)    TOKEN="$2"; shift 2 ;;
    --register) REGISTER="$2"; shift 2 ;;
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
    --quic-listen)  QUIC_LISTEN="$2"; shift 2 ;;
    --quic-ca)      QUIC_CA="$2"; shift 2 ;;
    --quic-insecure) QUIC_INSECURE=1; shift ;;
    --quic-binary)  QUIC_BINARY="$2"; shift 2 ;;
    -h|--help)  sed -n '2,40p' "$0"; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || die "请用 root 执行"
[ -n "$PANEL" ] || die "--panel 必填（面板基址，如 https://panel.example.com）"
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

# ---- 令牌：没给就从面板注册接口现场签发 ----
if [ -z "$TOKEN" ]; then
  [ -n "$REGISTER" ] || die "--token 与 --register 二选一"
  [ -n "$ADDRESS" ] || die "--register 需要同时给 --address"
  log "向面板注册节点 $REGISTER ..."
  body=$(curl -fsS -X POST "$PANEL/api/nodes" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$REGISTER\",\"address\":\"$ADDRESS\",\"port\":$PORT,\"protocol\":\"$PROTOCOL\"}") \
    || die "注册失败：检查面板地址与网络"
  TOKEN=$(printf '%s' "$body" | grep -o '"token":"[^"]*"' | head -1 | cut -d'"' -f4)
  [ -n "$TOKEN" ] || die "未从注册响应取到 token：$body"
  log "节点已创建，令牌已取得（面板节点页可查）"
fi

# ---- panel_url：http→ws、https→wss，统一拼 /agent/ws ----
case "$PANEL" in
  http://*)  WS="ws://${PANEL#http://}" ;;
  https://*) WS="wss://${PANEL#https://}" ;;
  ws://*|wss://*) WS="$PANEL" ;;
  *) die "--panel 需以 http(s):// 开头" ;;
esac
PANEL_URL="${WS%/}/agent/ws"

# ---- 取二进制：本地文件或按版本下载 ----
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
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
  # 校验：优先用发布 accompanying .sha256，其次 --sha256 手动值
  SUM_URL="$BASE/v$VERSION/ferry-agent_${VERSION}_linux_${ARCH}.tar.gz.sha256"
  if curl -fsSL "$SUM_URL" -o "$tmp/agent.sha256" 2>/dev/null; then
    (cd "$tmp" && sha256sum -c agent.sha256) || die "sha256 校验失败"
  elif [ -n "$SHA256" ]; then
    echo "$SHA256  $tmp/agent.tar.gz" | sha256sum -c - || die "sha256 校验失败"
  else
    log "警告：无 .sha256 且未指定 --sha256，跳过校验"
  fi
  tar -xzf "$tmp/agent.tar.gz" -C "$tmp"
  [ -f "$tmp/ferry-agent" ] || die "压缩包里没有 ferry-agent"
fi
install -m 0755 "$tmp/ferry-agent" "$INSTALL"

# ---- QUIC sidecar（E-18b）：可选发布件，不用 QUIC 传输不装 ----
QUIC_INSTALL=/usr/local/bin/ferry-quic
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
      (cd "$tmp" && sha256sum -c quic.sha256) || die "ferry-quic sha256 校验失败"
    else
      log "警告：ferry-quic 无伴随 .sha256，跳过校验"
    fi
    tar -xzf "$tmp/quic.tar.gz" -C "$tmp"
    [ -f "$tmp/ferry-quic" ] || die "压缩包里没有 ferry-quic"
  fi
  install -m 0755 "$tmp/ferry-quic" "$QUIC_INSTALL"
fi

# ---- 配置：/etc/ferry/agent.json（已存在则保留，不覆盖现场配置）----
mkdir -p /etc/ferry
if [ -f "$CONFIG" ]; then
  log "配置已存在，保留：$CONFIG（如需重写请先删除）"
else
  {
    echo '{'
    echo "  \"panel_url\": \"$PANEL_URL\","
    echo "  \"agent_id\": \"$AGENT_ID\","
    echo "  \"token\": \"$TOKEN\""
    # meta 带宽是 agent 配置校验必填项（缺省配置会启动失败）；其余元数据
    # agent 侧 applyMetaDefaults 补齐，FERRY_NODE_* 环境变量与面板可再改。
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
  } > "$CONFIG"
  chmod 600 "$CONFIG"
  log "已写配置 $CONFIG"
fi

# ---- systemd：agent 主服务（role=relay 追加 -role relay）----
ROLE_ARGS=""
[ "$ROLE" = "relay" ] && ROLE_ARGS=" -role relay"
# quic 插件拨 sidecar 桥：非缺省监听才写环境变量（缺省值两端对齐 127.0.0.1:7300）
QUIC_ENV=""
if [ "$QUIC" = 1 ] && [ "$QUIC_LISTEN" != "127.0.0.1:7300" ]; then
  QUIC_ENV="Environment=FERRY_QUIC_SIDECAR=$QUIC_LISTEN"
fi
unit=/etc/systemd/system/$SERVICE.service
cat > "$unit" <<EOF
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
systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null
systemctl restart "$SERVICE"
sleep 1
systemctl --no-pager --lines=5 status "$SERVICE" || true
log "完成：journalctl -u $SERVICE -f 查看连接日志"

# ---- systemd：ferry-quic sidecar（client 模式，本机桥 → QUIC 拨落地）----
if [ "$QUIC" = 1 ]; then
  QUIC_ARGS="client --listen $QUIC_LISTEN"
  [ -n "$QUIC_CA" ] && QUIC_ARGS="$QUIC_ARGS --ca $QUIC_CA"
  [ "$QUIC_INSECURE" = 1 ] && QUIC_ARGS="$QUIC_ARGS --insecure"
  quic_unit=/etc/systemd/system/ferry-quic.service
  cat > "$quic_unit" <<EOF
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
  systemctl daemon-reload
  systemctl enable ferry-quic >/dev/null
  systemctl restart ferry-quic
  sleep 1
  systemctl --no-pager --lines=5 status ferry-quic || true
  log "完成：journalctl -u ferry-quic -f 查看 sidecar 日志"
fi
