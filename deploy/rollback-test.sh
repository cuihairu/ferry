#!/usr/bin/env bash
# Server rollback test (F-1929): proves the property the rollback plan stands on --
# an OLD ferry-server binary keeps working on a NEWER schema, both read and write
# paths. AutoMigrate is additive-only (new tables / new columns, no destructive
# changes), so version rollback = swap the binary back and keep the data dir: GORM
# maps only its own struct fields, unknown extra columns stay NULL/default and
# unknown extra tables are simply never touched. Phase one seeds a deployment with
# the current binary (admin, node enrolled); the schema is then bumped forward by
# python3/sqlite3 (a new column on nodes, a whole new table with a row -- exactly
# the shape a newer version's AutoMigrate leaves behind) and the same binary --
# now the "old" one -- starts again: health, admin login, the enrolled node reads
# back, a SECOND node enrolls (write path on the newer schema), and the future
# table's row survives untouched. A further restart stays healthy (idempotent).
# The mechanics per deployment face are documented in deploy/README.md (compose:
# previous commit/image + rebuild; binary/systemd: keep the previous ferry-server
# and swap it back); a destructive-failure fallback is restore.sh with the
# pre-upgrade backup. Nothing is mocked; data lands only in a temp dir; any
# failing step prints the server log tail and exits non-zero. CI: test.yml's
# deploy-scripts job; locally the go toolchain and python3 are needed.
set -euo pipefail
cd "$(dirname "$0")/.."

# A random default port: a leftover server from an interrupted earlier run must
# never answer health for this run's process (same reasoning as upgrade-test.sh).
PORT=${ROLLBACK_PORT:-$(( (RANDOM % 200) + 18300 ))}
TMP=$(mktemp -d)
SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ "${FERRY_ROLLBACK_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[rollback] $*"; }
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
  FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/rollback.db" \
  FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=rollback-pass-012345 \
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
  echo "FAIL - server 健康检查超时（回滚后起不来）" >&2; dump; exit 1
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
  code=$(curl -s -o "$out" -w '%{http_code}' -X POST "$BASE/admin/login" \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"rollback-pass-012345"}')
  [ "$code" = 200 ] || { echo "FAIL - admin 登录失败（http $code）" >&2; dump; exit 1; }
  json_get token "$out"
}
enroll() { # NAME: issue a credential and enroll a node, echoes the http code
  local name="$1" code cred
  code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"rollback"}')
  [ "$code" = 201 ] || { echo "FAIL - 签发凭据失败（http $code）" >&2; dump; exit 1; }
  cred=$(json_get token "$TMP/enrolltok.json")
  curl -s -o "$TMP/enroll-$name.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
    -H "Authorization: Bearer $cred" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\",\"address\":\"rb.example.com\",\"port\":443,\"protocol\":\"vless\"}"
}

log "构建 ferry-server（回滚侧二进制）..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)

log "阶段一：初始化部署并写入数据 ..."
start_server
ADMIN_TOKEN=$(login "$TMP/login1.json")
expect "注册节点 rb-node" 201 "$(enroll rb-node)"
stop_server

log "把库面向前拨（新列+新表+行，模拟「新版本迁移已完成」的状态）..."
python3 - "$TMP/rollback.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
db.execute("ALTER TABLE nodes ADD COLUMN future_col TEXT")
db.execute("UPDATE nodes SET future_col = 'migrated-by-newer-version'")
db.execute("CREATE TABLE future_table (id INTEGER PRIMARY KEY, note TEXT)")
db.execute("INSERT INTO future_table (note) VALUES ('owned-by-newer-version')")
db.commit()
print("schema bumped forward: nodes.future_col + future_table(1 row)")
PY
log "ok - 库面已前拨"

log "阶段二：同一数据目录换回「旧版本」——回滚动作本身（老二进制对新库面）..."
start_server
expect "回滚后健康可达" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")"
ADMIN_TOKEN=$(login "$TMP/login2.json")
log "ok - 回滚后 admin 账号仍在"
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" "$BASE/api/nodes" -o "$TMP/nodes.json"
grep -q '"name":"rb-node"' "$TMP/nodes.json" \
  || { echo "FAIL - 回滚后节点数据丢失（读路径坏了）" >&2; dump; exit 1; }
log "ok - 回滚后已有节点可读（读路径无损）"
expect "回滚后新注册节点 rb-node2（写路径无损）" 201 "$(enroll rb-node2)"

log "未知对象必须原样保留（旧二进制不碰新版本的表/列）..."
python3 - "$TMP/rollback.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
row = db.execute("SELECT note FROM future_table WHERE id = 1").fetchone()
assert row and row[0] == "owned-by-newer-version", "future_table row lost: %r" % (row,)
n = db.execute("SELECT COUNT(*) FROM nodes WHERE future_col IS NOT NULL").fetchone()[0]
assert n >= 1, "nodes.future_col values lost"
print("future_table row + future_col values intact")
PY
log "ok - 新版本写入的表/列原样保留"

log "阶段三：回滚态再重启一次（幂等）..."
stop_server
start_server
expect "重启后健康可达" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")"
login "$TMP/login3.json" > /dev/null
log "ok - 幂等复跑无损"

log "回滚测试全部通过"
