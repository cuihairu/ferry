#!/usr/bin/env bash
# Server upgrade test (F-1928): really walks the documented upgrade path (拉新镜像重建 ->
# 新 ferry-server 二进制落在既有数据目录上 -> 启动 AutoMigrate 把库面向上迁). Phase one
# seeds a deployment with the current binary (admin first boot, node enrolled via the API);
# the data dir is then rolled back to an "older" schema (recent tables dropped, a recent
# column dropped, via python3/sqlite3) and the same new binary starts again -- the upgrade
# act. Assertions: server healthy after the upgrade, admin login still works (user data
# survived), the enrolled node survived, the dropped tables/columns are back (AutoMigrate
# is additive), and a further restart on the migrated schema stays healthy (idempotent).
# The binary-swap mechanics (image pull / compose recreate) is deploy tooling covered by
# compose-smoke/systemd-smoke; what this script owns is the schema+data upgrade assertions
# the runbook only had as prose. Nothing is mocked; data lands only in a temp dir; any
# failing step prints the server log tail and exits non-zero. CI: test.yml's deploy-scripts
# job; locally the go toolchain and python3 are needed.
set -euo pipefail
cd "$(dirname "$0")/.."

# A random default port: a leftover server from an interrupted earlier run must
# never answer health for this run's process (the impostor would turn every
# assertion into a spurious pass), so each run owns a fresh port instead.
PORT=${UPGRADE_PORT:-$(( (RANDOM % 200) + 18100 ))}
TMP=$(mktemp -d)
SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ "${FERRY_UPGRADE_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[upgrade] $*"; }
dump() { echo "--- server.log 尾部 ---" >&2; tail -n 40 "$TMP/server.log" >&2 || true; }
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
start_server() { # starts the binary against the fixed data dir, polls health; sets SERVER_PID and BASE globals
  FERRY_ADDR="127.0.0.1:$PORT" \
  FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/upgrade.db" \
  FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=upgrade-pass-012345 \
  FERRY_KEYFILE="$TMP/keys.env" \
  FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
  "$TMP/ferry-server" > "$TMP/server.log" 2>&1 &
  SERVER_PID=$!
  local code=""
  for i in $(seq 1 40); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/health" || true)
    [ "$code" = 200 ] && { BASE="http://127.0.0.1:$PORT"; return 0; }
    sleep 0.5
  done
  echo "FAIL - server 健康检查超时（升级后起不来）" >&2; dump; exit 1
}
stop_server() {
  # Tolerant kill: a server that already died on its own must not abort the
  # run here (set -e); the health polls above are the real liveness assertions.
  kill "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
  SERVER_PID=""
}
login() { # admin login, echoes the token
  local out="$1" code
  code=$(curl -s -o "$out" -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/admin/login" \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"upgrade-pass-012345"}')
  [ "$code" = 200 ] || { echo "FAIL - admin 登录失败（http $code）" >&2; dump; exit 1; }
  json_get token "$out"
}

log "构建 ferry-server（升级目标版本）..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)

log "阶段一：初始化旧部署并写入数据 ..."
start_server
ADMIN_TOKEN=$(login "$TMP/login1.json")
code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"upgrade"}')
expect "签发注册凭据" 201 "$code"
CRED=$(json_get token "$TMP/enrolltok.json")
code=$(curl -s -o "$TMP/enroll.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"upgrade-node","address":"up.example.com","port":443,"protocol":"vless"}')
expect "注册节点 upgrade-node" 201 "$code"
stop_server

log "把库面回拨成旧版本口径（删近期表+近期列，模拟老部署待升级状态）..."
python3 - "$TMP/upgrade.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
newer_tables = ["node_traffic_logs", "probe_reports", "node_upgrades",
                "cost_records", "bill_imports", "events", "backups"]
for t in newer_tables:
    db.execute("DROP TABLE IF EXISTS %s" % t)
cols = [r[1] for r in db.execute("PRAGMA table_info(nodes)")]
assert "monthly_traffic_quota_bytes" in cols, "nodes.monthly_traffic_quota_bytes missing"
db.execute("ALTER TABLE nodes DROP COLUMN monthly_traffic_quota_bytes")
db.commit()
print("rolled back:", ", ".join(newer_tables), "+ nodes.monthly_traffic_quota_bytes")
PY
log "ok - 库面已回拨"

log "阶段二：同一数据目录起「新版本」——升级动作本身 ..."
start_server
expect "升级后健康可达" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")"
ADMIN_TOKEN=$(login "$TMP/login2.json")
log "ok - 升级后 admin 账号仍在（用户数据存活）"
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$BASE/api/nodes" -o "$TMP/nodes.json"
grep -q '"name":"upgrade-node"' "$TMP/nodes.json" \
  || { echo "FAIL - 升级后节点数据丢失" >&2; dump; exit 1; }
log "ok - 升级后节点数据仍在"

log "AutoMigrate 必须把删掉的表/列补回来 ..."
python3 - "$TMP/upgrade.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
names = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
for t in ["node_traffic_logs", "probe_reports", "node_upgrades",
          "cost_records", "bill_imports", "events", "backups"]:
    assert t in names, "table %s not recreated" % t
cols = [r[1] for r in db.execute("PRAGMA table_info(nodes)")]
assert "monthly_traffic_quota_bytes" in cols, "column not re-added"
print("migrated back up: 7 tables + nodes.monthly_traffic_quota_bytes")
PY
log "ok - 库面向上迁移完成"

log "阶段三：迁完的库面上再重启一次（AutoMigrate 幂等复跑）..."
stop_server
start_server
expect "重启后健康可达" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")"
login "$TMP/login3.json" > /dev/null
log "ok - 幂等复跑无损"

log "升级测试全部通过"
