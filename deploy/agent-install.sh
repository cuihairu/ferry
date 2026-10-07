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
#
# 令牌还没有？在面板机上用 --register 顺手建节点拿令牌：
#   agent-install.sh --panel https://panel.example.com --register hk1 \
#     --address hk1.example.com --port 443 --protocol vless
set -euo pipefail

PANEL="" TOKEN="" REGISTER="" ADDRESS="" PORT=443 PROTOCOL=vless
AGENT_ID="$(hostname)" VERSION="latest"
BASE="https://github.com/cuihairu/ferry/releases/download"
BINARY="" SHA256="" CA="" CERT="" KEY=""
CONFIG=/etc/ferry/agent.json INSTALL=/usr/local/bin/ferry-agent SERVICE=ferry-agent

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
    --config)   CONFIG="$2"; shift 2 ;;
    --install)  INSTALL="$2"; shift 2 ;;
    -h|--help)  sed -n '2,20p' "$0"; exit 0 ;;
    *) die "unknown arg: $1" ;;
  esac
done

[ "$(id -u)" = 0 ] || die "请用 root 执行"
[ -n "$PANEL" ] || die "--panel 必填（面板基址，如 https://panel.example.com）"

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
    if [ -n "$CA" ] || [ -n "$CERT" ] || [ -n "$KEY" ]; then
      [ -n "$CA" ] && [ -n "$CERT" ] && [ -n "$KEY" ] || die "mTLS 需 --ca/--cert/--key 三者齐备"
      for f in "$CA" "$CERT" "$KEY"; do [ -f "$f" ] || die "证书文件不存在：$f"; done
      echo ',  "tls": {'
      echo "    \"ca_file\": \"$CA\","
      echo "    \"cert_file\": \"$CERT\","
      echo "    \"key_file\": \"$KEY\""
      echo '  }'
    fi
    echo '}'
  } > "$CONFIG"
  chmod 600 "$CONFIG"
  log "已写配置 $CONFIG"
fi

# ---- systemd 服务 ----
unit=/etc/systemd/system/$SERVICE.service
cat > "$unit" <<EOF
[Unit]
Description=ferry agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$INSTALL -config $CONFIG
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
