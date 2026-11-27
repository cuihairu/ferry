#!/usr/bin/env bash
# Server-restart agent reconnection test (F-1931): the operational claim "Server 重启后
# Agent 能重新连接，节点侧零操作" (MIGRATION-RUNBOOK 步骤四) as a real regression -- the
# unit tests cover the reconnect loop in isolation (link_test.go), but nothing killed a
# real ferry-server under a connected real agent and asserted the node comes back online
# with zero node-side operations. Flow: build both binaries; start the server; enroll a
# credential and register a node; start a real agent dialing the real WebSocket until
# agents_online==1; SIGTERM the server (health goes unreachable -- the outage window);
# restart the SAME binary on the SAME data dir (the restart act; node token persists in
# the DB); poll health until agents_online is back to 1 -- the agent's exponential
# backoff (1s->30s) must carry it through the outage with no node-side touch. Nothing
# is mocked; data lands only in a temp dir; any failing step prints service log tails
# and exits non-zero. CI: test.yml's deploy-scripts job; locally the go toolchain is
# needed.
set -euo pipefail
cd "$(dirname "$0")/.."

# A random default port: a leftover server from an interrupted earlier run must
# never answer health for this run's process (same reasoning as upgrade-test.sh).
PORT=${RECONNECT_PORT:-$(( (RANDOM % 200) + 18500 ))}
TMP=$(mktemp -d)
SERVER_PID="" AGENT_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null || true
  [ "${FERRY_RECONNECT_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[reconnect] $*"; }
dump() {
  for f in server.log server2.log agent.log; do
    [ -f "$TMP/$f" ] && { echo "--- $f 尾部 ---" >&2; tail -n 40 "$TMP/$f" >&2 || true; }
  done
}
expect() { # $1=description $2=expected $3=actual
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
json_get() { # jq/python3 first, falling back to text extraction (same ladder as e2e-smoke.sh)
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
agents_online_count() { # reads the nested checks.agents_online
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
poll_health() { # WANT: polls until agents_online==WANT (at most ~30s, reconnect backoff up to 8-16s)
  local want="$1" n=""
  for i in $(seq 1 60); do
    curl -s "$BASE/api/health" -o "$TMP/health-poll.json" 2>/dev/null || true
    n=$(agents_online_count "$TMP/health-poll.json")
    [ "$n" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$n"
}
start_server() { # starts the binary against the fixed data dir; sets SERVER_PID and BASE globals
  FERRY_ADDR="127.0.0.1:$PORT" \
  FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/reconnect.db" \
  FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=reconnect-pass-012345 \
  FERRY_KEYFILE="$TMP/keys.env" \
  FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
  FERRY_HEARTBEAT_SEC=5 \
  "$1" > "$TMP/server.log" 2>&1 &
  SERVER_PID=$!
  local code=""
  for i in $(seq 1 40); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/health" || true)
    [ "$code" = 200 ] && { BASE="http://127.0.0.1:$PORT"; return 0; }
    sleep 0.5
  done
  echo "FAIL - server 健康检查超时" >&2; dump; exit 1
}

log "构建 ferry-server / ferry-agent 二进制 ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
(cd agent && go build -o "$TMP/ferry-agent" ./cmd/agent)

log "阶段一：server + agent 全链路在线 ..."
start_server "$TMP/ferry-server"
curl -s -o "$TMP/login.json" -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"reconnect-pass-012345"}'
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 管理员登录失败" >&2; dump; exit 1; }
code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"reconnect"}')
expect "签发注册凭据" 201 "$code"
CRED=$(json_get token "$TMP/enrolltok.json")
code=$(curl -s -o "$TMP/enroll.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"reconnect-node","address":"rc.example.com","port":443,"protocol":"vless"}')
expect "注册节点 reconnect-node" 201 "$code"
NODE_TOKEN=$(json_get token "$TMP/enroll.json")
cat > "$TMP/agent.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "reconnect-agent",
  "token": "$NODE_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "procs": []
}
EOF
"$TMP/ferry-agent" -config "$TMP/agent.json" > "$TMP/agent.log" 2>&1 &
AGENT_PID=$!
expect "agent 上线（健康计数=1）" "1" "$(poll_health 1)"

log "阶段二：杀掉 server——故障窗口（健康不可达）..."
kill "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""
for i in $(seq 1 10); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/api/health" 2>/dev/null || true)
  [ "$code" != 200 ] && break
  sleep 0.5
done
[ "$code" != 200 ] || { echo "FAIL - server 进程已杀但健康仍 200（端口被顶替？）" >&2; dump; exit 1; }
log "ok - 故障窗口成立（健康不可达，http=$code）"

log "阶段三：同一数据目录重启 server——节点侧零操作，等 agent 按退避回池 ..."
start_server "$TMP/ferry-server"
expect "server 重启后 agent 自动重连（健康计数=1）" "1" "$(poll_health 1)"
log "reconnect 测试全部通过"
