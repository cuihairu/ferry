#!/usr/bin/env bash
# Data-plane outage test (F-1932): the design claim "Server 不可用时数据面继续运行
# （agent 本地自治：隧道/用户流量继续，管理面断）" (《面板可用性设计》§1) as a real
# regression. The data plane in the rig is the agent's managed kernel proc; its
# supervision is purely local (procs.Manager supervise loop -- no panel involvement),
# so while the panel is DOWN a crashing kernel must still be crash-restarted by the
# agent, and the agent itself must stay alive. Flow: build both binaries; start the
# server; enroll + register; start a real agent with a stub kernel that exits by
# itself every ~4s (reload: restart, so crash supervision owns the restarts); agent
# online; SIGTERM the server (outage window); DURING the outage the stub keeps
# dying and the agent keeps restarting it locally (kernel-runs.log grows well past
# the first start) and the agent process stays alive; restart the server and the
# link comes back (agents_online==1) with the proc still running. Offline heartbeat
# buffering is unit-covered (spool_test.go); what this script owns is the "data
# plane survives the outage" end-to-end assertion. Nothing is mocked; data lands
# only in a temp dir; any failing step prints service log tails and exits non-zero.
# CI: test.yml's deploy-scripts job; locally the go toolchain is needed.
set -euo pipefail
cd "$(dirname "$0")/.."

# A random default port: a leftover server from an interrupted earlier run must
# never answer health for this run's process (same reasoning as upgrade-test.sh).
PORT=${DATAPLANE_PORT:-$(( (RANDOM % 200) + 18700 ))}
TMP=$(mktemp -d)
SERVER_PID="" AGENT_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null || true
  [ "${FERRY_DATAPLANE_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[dataplane] $*"; }
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
poll_health() { # WANT: polls until agents_online==WANT (at most ~30s)
  local want="$1" n=""
  for i in $(seq 1 60); do
    curl -s "$BASE/api/health" -o "$TMP/health-poll.json" 2>/dev/null || true
    n=$(agents_online_count "$TMP/health-poll.json")
    [ "$n" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$n"
}
wait_kernel_runs() { # prints 1 once the log has >= MIN "up" lines, else 0 (at most ~30s)
  # Boolean readiness, not the raw count: the stub kernel crashes every 4s and
  # the agent restarts it, so on a slow CI worker the count can already be 3+
  # by the time the agent finishes connecting. The claim under test is "the
  # data plane started", never "exactly one start".
  local min="$1" n=0
  for i in $(seq 1 60); do
    n=$(grep -c '^up ' "$TMP/kernel-runs.log" 2>/dev/null || true)
    if [ "$n" -ge "$min" ]; then printf '1'; return 0; fi
    sleep 0.5
  done
  printf '0'
}
start_server() { # starts the binary against the fixed data dir; sets SERVER_PID and BASE globals
  FERRY_ADDR="127.0.0.1:$PORT" \
  FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/dataplane.db" \
  FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=dataplane-pass-012345 \
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

log "启动 server 并注册节点 ..."
start_server "$TMP/ferry-server"
curl -s -o "$TMP/login.json" -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"dataplane-pass-012345"}'
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 管理员登录失败" >&2; dump; exit 1; }
code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"dataplane"}')
expect "签发注册凭据" 201 "$code"
CRED=$(json_get token "$TMP/enrolltok.json")
code=$(curl -s -o "$TMP/enroll.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"dataplane-node","address":"dp.example.com","port":443,"protocol":"vless"}')
expect "注册节点 dataplane-node" 201 "$code"
NODE_TOKEN=$(json_get token "$TMP/enroll.json")

log "agent + 会自崩的 stub 内核（4 秒退出，reload: restart 交给本地监护）..."
# Stub kernel: exits by itself every ~4s -- during the outage every exit is a crash
# the agent must restart LOCALLY (supervision has no panel dependency, procs.Manager).
cat > "$TMP/stub-kernel" <<EOF
#!/bin/sh
echo "up \$(date +%s)" >> "$TMP/kernel-runs.log"
sleep 4
EOF
chmod +x "$TMP/stub-kernel"
: > "$TMP/kernel-runs.log"
: > "$TMP/k1.json"
cat > "$TMP/agent.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "dataplane-agent",
  "token": "$NODE_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "procs": [ {
    "name": "k1", "kind": "hysteria2", "exec": "$TMP/stub-kernel",
    "config_path": "$TMP/k1.json", "reload": "restart"
  } ]
}
EOF
"$TMP/ferry-agent" -config "$TMP/agent.json" > "$TMP/agent.log" 2>&1 &
AGENT_PID=$!
expect "agent 上线（健康计数=1）" "1" "$(poll_health 1)"
expect "数据面 proc 已起（kernel-runs>=1）" "1" "$(wait_kernel_runs 1)"

log "杀掉 server——故障窗口开始 ..."
kill "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""
sleep 1

log "故障窗口内：stub 内核继续自崩，agent 必须本地监护继续拉起（数据面不停）..."
# The stub dies every ~4s; with 1s initial restart backoff the log must grow well
# past the first start within ~18s of outage -- every extra "up" line is a local
# restart that happened with the panel completely unreachable.
BASE="" # guard: nothing below may depend on the panel
before=$(grep -c '^up ' "$TMP/kernel-runs.log")
sleep 18
during=$(grep -c '^up ' "$TMP/kernel-runs.log")
if [ "$during" -ge $((before + 2)) ]; then
  log "ok - 故障窗口内本地重启继续（kernel-runs $before -> $during）"
else
  echo "FAIL - server 宕机后 agent 本地监护停摆（kernel-runs $before -> $during）" >&2
  dump; exit 1
fi
kill -0 "$AGENT_PID" 2>/dev/null || { echo "FAIL - agent 在故障窗口内退出" >&2; dump; exit 1; }
log "ok - agent 进程存活（本地自治未依赖面板）"

log "面板恢复：同一数据目录重启 server，链路回池且数据面 proc 仍在 ..."
start_server "$TMP/ferry-server"
expect "重连（健康计数=1）" "1" "$(poll_health 1)"
after=$(grep -c '^up ' "$TMP/kernel-runs.log")
if [ "$after" -ge "$during" ]; then
  log "ok - 重连后数据面 proc 仍在跑（kernel-runs=$after）"
else
  echo "FAIL - 重连后数据面 proc 丢了（kernel-runs $during -> $after）" >&2
  dump; exit 1
fi
log "dataplane 测试全部通过"
