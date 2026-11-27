#!/usr/bin/env bash
# Disconnect-replay integration test (F-1255): the designed re-report path for
# ROLE-process reports (docs/design/agent协议参考.md §3 table row 2) as a real
# regression. The standalone -role probe process never talks to the panel; it
# appends report envelopes to <spool_dir>/probe.out, and the core's spool.Watch
# forwards them over the single node connection. While the panel is DOWN the
# core cannot forward (sendIfConnected errors, the file offset does not move);
# after recovery the buffered lines replay, the panel's FERRY-004 window
# dedupes the replayed envelope IDs, and the offset advances so the backlog is
# sent exactly once. Flow: real server + real core agent (spool_dir set, so
# built-in probing is off) + real -role probe process against a local UDP
# echo; verify reports land pre-outage; SIGTERM the server; during the outage
# the role keeps probing (reports would be unreachable without replay); restart
# the server; assert reports with probed_at INSIDE the outage window show up in
# GET /api/probe-reports (only the replay path can deliver those), and that the
# count then grows only at normal cadence (no backlog re-replay storm). The
# traffic-report leg is intentionally NOT covered here: at-most-once drop is
# the pinned design (F-1251, docs/design/agent架构设计.md §8). Nothing mocked;
# data lands only in a temp dir; failures print log tails and exit 1.
# CI: test.yml deploy-scripts.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${REPLAY_PORT:-$(( (RANDOM % 200) + 18700 ))}
UDPPORT=$(( (RANDOM % 200) + 18900 ))
TMP=$(mktemp -d)
SERVER_PID="" CORE_PID="" ROLE_PID="" ECHO_PID=""
cleanup() {
  for pid in "$ECHO_PID" "$ROLE_PID" "$CORE_PID" "$SERVER_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[replay] $*"; }
dump() {
  for f in server.log core.log role.log echo.log; do
    [ -f "$TMP/$f" ] && { echo "--- $f 尾部 ---" >&2; tail -n 30 "$TMP/$f" >&2 || true; }
  done
}
expect() {
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
json_get() { # FIELD FILE
  python3 -c 'import json,sys
try: print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception: pass' "$1" "$2" 2>/dev/null || true
}
agents_online_count() {
  python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1]))["checks"]["agents_online"])
except Exception: pass' "$TMP/health-poll.json" 2>/dev/null || true
}
poll_health() { # WANT TRIES
  local want="$1" tries="$2" n=""
  for i in $(seq 1 "$tries"); do
    curl -s --max-time 3 "$BASE/api/health" -o "$TMP/health-poll.json" 2>/dev/null || true
    n=$(agents_online_count)
    [ "$n" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$n"
}
report_count() { # total probe report rows
  python3 -c 'import json,sys
try: print(len(json.load(open(sys.argv[1]))))
except Exception: print(0)' "$TMP/reports.json" 2>/dev/null || printf '0'
}
in_window_count() { # rows with probed_at inside the recorded outage window
  python3 -c 'import json,sys,datetime
lo=datetime.datetime.fromisoformat(sys.argv[1].replace("Z","+00:00"))
hi=datetime.datetime.fromisoformat(sys.argv[2].replace("Z","+00:00"))
try: rows=json.load(open(sys.argv[3]))
except Exception: rows=[]
n=0
for r in rows:
    t=datetime.datetime.fromisoformat(r["probed_at"].replace("Z","+00:00"))
    if lo <= t <= hi: n+=1
print(n)' "$1" "$2" "$TMP/reports.json" 2>/dev/null || printf '0'
}
fetch_reports() {
  curl -s --max-time 5 "$BASE/api/probe-reports?limit=1000" -H "Authorization: Bearer $ADMIN_TOKEN" \
    -o "$TMP/reports.json" 2>/dev/null || true
}
wait_reports_in_window() { # LO HI TRIES: prints 1 once an in-window row arrives
  local lo="$1" hi="$2" tries="$3" n=0
  for i in $(seq 1 "$tries"); do
    fetch_reports
    n=$(in_window_count "$lo" "$hi")
    [ "$n" -ge 1 ] && { printf '1'; return 0; }
    sleep 2
  done
  printf '0'
}

log "构建 ferry-server / ferry-agent 二进制 ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
(cd agent && go build -o "$TMP/ferry-agent" ./cmd/agent)

log "启动 server + 注册节点 ..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/replay.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=replay-pass-012345 \
FERRY_KEYFILE="$TMP/keys.env" \
FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
FERRY_HEARTBEAT_SEC=5 \
  "$TMP/ferry-server" > "$TMP/server.log" 2>&1 &
SERVER_PID=$!
BASE="http://127.0.0.1:$PORT"
code=""
for i in $(seq 1 40); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health" || true)
  [ "$code" = 200 ] && break
  sleep 0.5
done
expect "server 可达" 200 "$code"

curl -s -o "$TMP/login.json" -X POST "$BASE/admin/login" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"replay-pass-012345"}'
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 管理员登录失败" >&2; dump; exit 1; }
curl -s -o "$TMP/enrolltok.json" -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"replay"}'
CRED=$(json_get token "$TMP/enrolltok.json")
curl -s -o "$TMP/enroll.json" -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"replay-node","address":"rp.example.com","port":443,"protocol":"vless"}'
NODE_TOKEN=$(json_get token "$TMP/enroll.json")

log "本地 UDP 回显端 + 核心进程（spool_dir 开启，内置探测关闭）..."
python3 - "$UDPPORT" > "$TMP/echo.log" 2>&1 <<'PY' &
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("127.0.0.1", int(sys.argv[1])))
while True:
    data, addr = s.recvfrom(2048)
    s.sendto(data, addr)
PY
ECHO_PID=$!

mkdir -p "$TMP/spool"
cat > "$TMP/core.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "replay-core",
  "token": "$NODE_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "spool_dir": "$TMP/spool",
  "procs": []
}
EOF
"$TMP/ferry-agent" -config "$TMP/core.json" > "$TMP/core.log" 2>&1 &
CORE_PID=$!
expect "核心 agent 上线" "1" "$(poll_health 1 30)"

log "独立 probe 角色进程（只写 spool，不连面板）..."
cat > "$TMP/role.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "replay-core",
  "token": "$NODE_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "spool_dir": "$TMP/spool",
  "probes": [ {
    "name": "replay-probe", "target_kind": "exit", "target": "127.0.0.1:$UDPPORT",
    "proto": "udp", "payload_hex": "aa", "expect_hex": "aa", "interval_sec": 5
  } ]
}
EOF
"$TMP/ferry-agent" -role probe -config "$TMP/role.json" > "$TMP/role.log" 2>&1 &
ROLE_PID=$!

# Pre-outage: the live pipeline must deliver at least one verdict.
pre=0
for i in $(seq 1 20); do
  fetch_reports
  pre=$(report_count)
  [ "$pre" -ge 1 ] && break
  sleep 2
done
expect "断线前探测结论已入账（>=1）" "1" "$([ "$pre" -ge 1 ] && printf 1 || printf 0)"
grep -q . "$TMP/spool/probe.out" && log "ok - spool 文件在写（$(wc -l < "$TMP/spool/probe.out") 行）" \
  || { echo "FAIL - spool 文件为空，角色未写 spool" >&2; dump; exit 1; }

log "杀掉 server——断线窗开始，角色继续探测、核心无法转发 ..."
DOWN_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
kill "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""
sleep 15
UP_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
kill -0 "$ROLE_PID" 2>/dev/null && log "ok - 角色进程断线窗内存活并持续写 spool（$(wc -l < "$TMP/spool/probe.out") 行）" \
  || { echo "FAIL - 角色进程在断线窗内退出" >&2; dump; exit 1; }
lines_at_recovery=$(wc -l < "$TMP/spool/probe.out")

log "重启 server（同一数据目录）：核心回连，spool 整段重放 ..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/replay.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=replay-pass-012345 \
FERRY_KEYFILE="$TMP/keys.env" \
FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
FERRY_HEARTBEAT_SEC=5 \
  "$TMP/ferry-server" >> "$TMP/server.log" 2>&1 &
SERVER_PID=$!
expect "回连（健康计数=1）" "1" "$(poll_health 1 40)"

# The replay proof: rows probed DURING the outage can only arrive through the
# spool replay -- the role has no panel connection and the core was cut off
# for the whole window.
expect "断线窗内探测的结论经重放入账（>=1）" "1" "$(wait_reports_in_window "$DOWN_AT" "$UP_AT" 30)"
fetch_reports
total1=$(report_count)
log "ok - 重放后总数=$total1（断线窗内落账 $(in_window_count "$DOWN_AT" "$UP_AT") 条）"

# No replay storm: the offset advanced, so the backlog is sent once and growth
# returns to the probe's own ~5s cadence (~2 rows / 12s). An unbounded
# re-replay regression would add the whole backlog every 2s watch cycle.
sleep 12
fetch_reports
total2=$(report_count)
if [ "$((total2 - total1))" -le 4 ]; then
  log "ok - 重放恰好一次（12s 内新增 $((total2 - total1)) 条，正常节奏）"
else
  echo "FAIL - 疑似无限重放（12s 内新增 $((total2 - total1)) 条）" >&2; dump; exit 1
fi
kill -0 "$ROLE_PID" 2>/dev/null && log "ok - 角色进程全程存活" \
  || { echo "FAIL - 角色进程中途退出" >&2; dump; exit 1; }

log "PASS - F-1255 断线补报集成（角色报告 spool 重放腿）：断线窗结论经重放入账且仅入账一次"
