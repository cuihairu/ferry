#!/usr/bin/env bash
# 端到端部署链路冒烟（FERRY-004 最小骨架）：真起 ferry-server 二进制（sqlite
# 落临时目录）+ 真 ferry-agent 二进制拨真实 WebSocket，一条链路走通
# 健康检查/库初始化 → 管理员首登与鉴权 → 签发一次性注册凭据 → 注册建节点
# → agent 握手上线（健康计数+1）→ agent 断线回落。全程不 mock，数据只落
# 临时目录；任一步失败打印 server 与 agent 日志尾部后非零退出。
# 评审 FERRY-004 十项验收中的骨架部分（部署/通信/配置与恢复的其余项按
# 台账后续增量补齐）。CI 见 test.yml deploy-scripts job；本地需 go 工具链。
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${E2E_PORT:-18099}
TMP=$(mktemp -d)
SERVER_PID="" AGENT_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[e2e] $*"; }
expect() { # $1=描述 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
dump() {
  echo "--- server 日志尾部 ---" >&2
  tail -n 40 "$TMP/server.log" >&2 || true
  echo "--- agent 日志尾部 ---" >&2
  tail -n 40 "$TMP/agent.log" >&2 || true
}
# json_get FILE KEY：优先 jq/python3，退回文本截取（与安装脚本同口径）
json_get() {
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
    v=$(grep -o "\"$f\":[[:space:]]*\"[^\"]*\"" "$file" 2>/dev/null | head -1 | cut -d'"' -f4)
  fi
  printf '%s' "$v"
}

# agents_online_count 取嵌套 checks.agents_online（健康计数）
agents_online_count() {
  local file="$1" v=""
  if command -v jq >/dev/null 2>&1; then
    v=$(jq -r '.checks.agents_online // empty' "$file" 2>/dev/null) || v=""
  elif command -v python3 >/dev/null 2>&1; then
    v=$(python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[1]))["checks"]["agents_online"])
except Exception:
    pass' "$file" 2>/dev/null) || v=""
  else
    v=$(grep -o '"agents_online":[0-9]*' "$file" 2>/dev/null | head -1 | cut -d: -f2)
  fi
  printf '%s' "$v"
}

# poll_health WANT：轮询健康直到 agents_online==WANT（最多 ~15 秒），回显计数
poll_health() {
  local want="$1" n=""
  for i in $(seq 1 30); do
    curl -s "$BASE/api/health" -o "$TMP/health-poll.json" 2>/dev/null || true
    n=$(agents_online_count "$TMP/health-poll.json")
    [ "$n" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$n"
}

log "构建 ferry-server / ferry-agent 二进制 ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
(cd agent && go build -o "$TMP/ferry-agent" ./cmd/agent)

log "启动 server（sqlite 落 $TMP，密钥文件同目录）..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/e2e.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=e2e-pass-0123456 \
FERRY_KEYFILE="$TMP/keys.env" \
FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
  "$TMP/ferry-server" > "$TMP/server.log" 2>&1 &
SERVER_PID=$!
BASE="http://127.0.0.1:$PORT"

for i in $(seq 1 40); do
  code=$(curl -s -o "$TMP/health.json" -w '%{http_code}' "$BASE/api/health" || true)
  [ "$code" = 200 ] && break
  sleep 0.5
done
expect "server 健康检查可达" 200 "$code"
expect "健康返回三态之一（新库 degraded=从未备份，属正常）" "degraded" "$(json_get status "$TMP/health.json")"
grep -q '"db_writable":true' "$TMP/health.json" \
  || { echo "FAIL - 数据库不可写" >&2; dump; exit 1; }
log "ok - 数据库可写（sqlite 初始化完成）"

log "管理员首启账号登录 ..."
code=$(curl -s -o "$TMP/login.json" -w '%{http_code}' -X POST "$BASE/admin/login" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"e2e-pass-0123456"}')
expect "管理员登录成功" 200 "$code"
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 登录响应缺 token" >&2; dump; exit 1; }
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/admin/login" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"wrong-pass"}')
expect "错误密码拒绝" 401 "$code"

log "管理面鉴权与一次性注册凭据签发 ..."
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/nodes")
expect "无凭据访问管理面拒绝" 401 "$code"

log "手动触发数据库备份，随后健康应转 ok ..."
code=$(curl -s -o "$TMP/backup.json" -w '%{http_code}' "$BASE/admin/backup/db" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "备份触发成功" 200 "$code"
for i in $(seq 1 10); do
  curl -s "$BASE/api/health" -o "$TMP/health2.json" 2>/dev/null || true
  [ "$(json_get status "$TMP/health2.json")" = ok ] && break
  sleep 0.5
done
expect "备份后健康转 ok" ok "$(json_get status "$TMP/health2.json")"
code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"e2e"}')
expect "签发注册凭据成功" 201 "$code"
CRED=$(json_get token "$TMP/enrolltok.json")
[ -n "$CRED" ] || { echo "FAIL - 凭据签发响应缺 token" >&2; dump; exit 1; }

log "节点注册（凭据换新令牌）..."
code=$(curl -s -o "$TMP/enroll.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"e2e-node","address":"e2e.example.com","port":443,"protocol":"vless"}')
expect "注册成功" 201 "$code"
NODE_TOKEN=$(json_get token "$TMP/enroll.json")
[ -n "$NODE_TOKEN" ] || { echo "FAIL - 注册响应缺 token" >&2; dump; exit 1; }
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"e2e-node-2","address":"e2e.example.com","port":443,"protocol":"vless"}')
expect "凭据一次性（重放拒绝）" 401 "$code"

log "启动 agent 拨面板（真实 WebSocket 握手）..."
cat > "$TMP/agent.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-agent",
  "token": "$NODE_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 }
}
EOF
"$TMP/ferry-agent" -config "$TMP/agent.json" > "$TMP/agent.log" 2>&1 &
AGENT_PID=$!

expect "agent 上线（健康计数=1）" "1" "$(poll_health 1)"
log "断线回落 ..."
kill "$AGENT_PID"; AGENT_PID=""
expect "agent 断线后计数回落" "0" "$(poll_health 0)"

log "PASS：部署→认证→注册→通信→断线链路全通"

# ---- compose 配置检查（FERRY-004 第 1 项；无 docker 的环境跳过）----
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  log "校验 docker compose 配置 ..."
  if docker compose --env-file deploy/ferry.env.example \
      -f deploy/docker-compose.yml config -q 2> "$TMP/compose.err"; then
    log "ok - compose 配置合法"
  else
    echo "FAIL - compose 配置校验失败：" >&2
    cat "$TMP/compose.err" >&2
    exit 1
  fi
else
  log "skip - 本机无 docker，跳过 compose 配置检查"
fi
