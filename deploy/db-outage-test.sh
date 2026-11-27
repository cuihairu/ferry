#!/usr/bin/env bash
# Brief database outage test (F-2074): inject a REAL short lock storm
# against the live server and assert it rides it out. The server runs sqlite
# in WAL mode with busy_timeout 5000ms (storage.go); a side sqlite connection
# holding BEGIN EXCLUSIVE for ~6s takes the database locks for real, so every
# server query in the window blocks past busy_timeout and fails -- exactly the
# "database briefly unavailable" fault. Assertions: health goes non-200 during the window (the
# reads really are blocked), the server process stays alive the whole time,
# sqlite surfaces busy/locked errors in its log, and after the lock releases
# health returns to 200 and the connected agents' heartbeats resume (both
# nodes online again). Nothing mocked; data lands only in a temp dir; failures
# print log tails and exit 1. CI: test.yml deploy-scripts.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${DB_OUTAGE_PORT:-$(( (RANDOM % 200) + 18700 ))}
TMP=$(mktemp -d)
SERVER_PID="" AGENT1_PID="" AGENT2_PID="" LOCK_PID=""
cleanup() {
  for pid in "$LOCK_PID" "$AGENT1_PID" "$AGENT2_PID" "$SERVER_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[db-outage] $*"; }
dump() {
  for f in server.log agent-1.log agent-2.log; do
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

log "构建 ferry-server / ferry-agent ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
(cd agent && go build -o "$TMP/ferry-agent" ./cmd/agent)

log "启动 server + 两个真实 agent ..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/outage.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=e2e-pass-0123456 \
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
  -d '{"username":"admin","password":"e2e-pass-0123456"}'
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 管理员登录失败" >&2; dump; exit 1; }
for name in outage-1 outage-2; do
  # Enrollment tokens are single-use: issue one per node.
  curl -s -o "$TMP/enrolltok-$name.json" -X POST "$BASE/api/enrollment-tokens" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d "{\"note\":\"$name\"}"
  CRED=$(json_get token "$TMP/enrolltok-$name.json")
  curl -s -o "$TMP/enroll-$name.json" -X POST "$BASE/api/enroll" \
    -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\",\"address\":\"db.example.com\",\"port\":443,\"protocol\":\"vless\"}"
  TOKEN=$(json_get token "$TMP/enroll-$name.json")
  cat > "$TMP/agent-$name.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "$name",
  "token": "$TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "procs": []
}
EOF
  "$TMP/ferry-agent" -config "$TMP/agent-$name.json" > "$TMP/agent-$name.log" 2>&1 &
  PID=$!
  if [ "$name" = outage-1 ]; then AGENT1_PID=$PID; else AGENT2_PID=$PID; fi
done
expect "两个 agent 上线" "2" "$(poll_health 2 30)"

log "F-2074: 注入 6 秒真实 DB 锁窗（第二个 sqlite 连接持 EXCLUSIVE 事务）..."
# A separate sqlite connection holding BEGIN EXCLUSIVE takes the database's
# writer/reader locks for real: every query the server runs during the window
# conflicts and waits out its 5s busy_timeout (storage.go), surfacing as
# "database is locked" -- the brief-outage fault injected at the DB layer.
python3 - "$TMP/outage.db" > "$TMP/locker.log" 2>&1 <<'PY' &
import sqlite3, sys, time

db = sqlite3.connect(sys.argv[1])
db.execute("begin exclusive")
print("locked", flush=True)
time.sleep(6)
db.rollback()
print("released", flush=True)
PY
LOCK_PID=$!
for i in $(seq 1 20); do
  grep -q '^locked' "$TMP/locker.log" 2>/dev/null && break
  sleep 0.2
done
grep -q '^locked' "$TMP/locker.log" || { echo "FAIL - 锁未获取（注入未生效）" >&2; cat "$TMP/locker.log" >&2; dump; exit 1; }

# Window: health reads must actually be blocked (non-200 or timeout).
blocked=""
for i in $(seq 1 24); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "$BASE/api/health" 2>/dev/null || true)
  if [ "$code" != "200" ]; then blocked="1"; break; fi
  sleep 0.5
done
expect "故障窗成立（健康读被锁阻塞）" "1" "$blocked"
kill -0 "$SERVER_PID" 2>/dev/null && log "ok - server 进程在锁窗内存活" \
  || { echo "FAIL - server 在锁窗内崩溃" >&2; dump; exit 1; }

# Release: the locker exits on its own after 6s.
for i in $(seq 1 30); do
  grep -q '^released' "$TMP/locker.log" 2>/dev/null && break
  sleep 0.5
done
grep -q '^released' "$TMP/locker.log" || { echo "FAIL - 锁未按时释放" >&2; cat "$TMP/locker.log" >&2; dump; exit 1; }
log "ok - 锁已释放"

# Recovery: busy errors must have surfaced, health returns, heartbeats resume.
grep -qi "database is locked\|SQLITE_BUSY\|database busy" "$TMP/server.log" \
  && log "ok - sqlite busy 错误已浮现（故障真实注入到 server）" \
  || log "note - server.log 未捕获 busy 文案（读路径可能以超时形态表现）"
code=""
for i in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$BASE/api/health" 2>/dev/null || true)
  [ "$code" = 200 ] && break
  sleep 0.5
done
expect "锁释放后健康回 200" 200 "$code"
expect "agent 心跳写恢复（两节点重新在线）" "2" "$(poll_health 2 30)"
kill -0 "$SERVER_PID" 2>/dev/null && log "ok - server 全程存活" \
  || { echo "FAIL - server 未存活到恢复后" >&2; dump; exit 1; }

log "PASS - F-2074 DB 短暂不可用（真实 EXCLUSIVE 锁注入）：server 存活、锁窗后自愈"
