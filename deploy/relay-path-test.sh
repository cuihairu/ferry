#!/usr/bin/env bash
# Relay tunnel + Path probe + fault/recovery smoke (F-2044/F-2045/F-2050/F-2051):
# the acceptance rows the generic e2e-smoke.sh never covered. Real binaries,
# real WebSocket, real TCP data path, real probe verdicts; nothing mocked at
# the protocol layer.
# Flow: build server/agent; start the server; enroll three nodes (entry, relay,
# landing) with data-plane ports; start three core agents (all online); run the
# relay role as a standalone process (front listener -> tls-camo -> local TLS
# echo = the landing listener); drive a TCP byte round-trip THROUGH the relay
# front listener (F-2044); create an entry -> relay -> landing Path via the
# panel API and dispatch a TCP probe from the entry agent at the landing port;
# assert the probe report lands reachable/healthy and resolves to the landing
# node, and that path health turns healthy (F-2045); then kill the TLS echo
# (probe goes sick, path degrades) and restart it (path recovers), then kill
# the landing agent (node offline, path degrades) and reconnect it (path
# recovers) -- F-2050 fault handling and F-2051 recovery rejoin. Data lands
# only in a temp dir; any failing step prints service log tails and exits
# non-zero. CI: test.yml's deploy-scripts job; locally the go toolchain is needed.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${RELAY_PATH_PORT:-$(( (RANDOM % 200) + 18400 ))}
TLS_PORT=$((PORT + 1))
RELAY_PORT=$((PORT + 2))
TMP=$(mktemp -d)
SERVER_PID="" ENTRY_PID="" LANDING_PID="" RELAY_CORE_PID="" RELAY_PID="" TLS_PID=""
cleanup() {
  for pid in "$RELAY_PID" "$RELAY_CORE_PID" "$LANDING_PID" "$ENTRY_PID" "$SERVER_PID" "$TLS_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[relay-path] $*"; }
expect() {
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
dump() {
  for f in server.log agent-entry.log agent-relay.log agent-landing.log relay-role.log tls-echo.log; do
    if [ -f "$TMP/$f" ]; then
      echo "--- $f 尾部 ---" >&2
      tail -n 40 "$TMP/$f" >&2 || true
    fi
  done
}
json_get() { # FIELD FILE
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg k "$1" '.[$k] // empty' "$2" 2>/dev/null || true
  else
    python3 -c 'import json,sys
try: print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception: pass' "$1" "$2" 2>/dev/null || true
  fi
}
poll_health() { # WANT
  local want="$1" n=""
  for i in $(seq 1 40); do
    curl -s "$BASE/api/health" -o "$TMP/health.json" 2>/dev/null || true
    n=$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1]))["checks"]["agents_online"])
except Exception: pass' "$TMP/health.json" 2>/dev/null || true)
    [ "$n" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$n"
}

log "构建 ferry-server / ferry-agent ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
(cd agent && go build -o "$TMP/ferry-agent" ./cmd/agent)

log "启动 server ..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/e2e.db" \
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

code=$(curl -s -o "$TMP/login.json" -w '%{http_code}' -X POST "$BASE/admin/login" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"e2e-pass-0123456"}')
expect "管理员登录" 200 "$code"
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 登录响应缺 token" >&2; dump; exit 1; }

# enroll_node NAME PORT: issue a credential, enroll address=127.0.0.1 with the
# given data-plane port, echo the node token (progress lines go to stderr so
# command substitution captures only the token).
enroll_node() {
  local name="$1" port="$2" c t
  c=$(curl -s -o "$TMP/enrolltok-$name.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    -d "{\"note\":\"$name\"}")
  expect "签发 $name 凭据" 201 "$c" >&2
  t=$(json_get token "$TMP/enrolltok-$name.json")
  c=$(curl -s -o "$TMP/enroll-$name.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
    -H "Authorization: Bearer $t" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\",\"address\":\"127.0.0.1\",\"port\":$port,\"protocol\":\"vless\"}")
  expect "注册 $name" 201 "$c" >&2
  json_get token "$TMP/enroll-$name.json"
}
ENTRY_TOKEN=$(enroll_node e2e-entry "$PORT")
RELAY_TOKEN=$(enroll_node e2e-relay "$RELAY_PORT")
LANDING_TOKEN=$(enroll_node e2e-landing "$TLS_PORT")
ENTRY_ID=$(json_get id "$TMP/enroll-e2e-entry.json")
RELAY_ID=$(json_get id "$TMP/enroll-e2e-relay.json")
LANDING_ID=$(json_get id "$TMP/enroll-e2e-landing.json")

# Landing TLS echo = the landing listener stand-in: real TLS + byte echo, cert
# pinned by CA file in the relay config (self-signed, no system-root reliance).
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=127.0.0.1' -addext 'subjectAltName=IP:127.0.0.1' \
  -keyout "$TMP/tls.key" -out "$TMP/tls.crt" > "$TMP/openssl.log" 2>&1
mkdir -p "$TMP/tls-echo-src"
cat > "$TMP/tls-echo-src/main.go" <<'EOF'
package main

import (
	"crypto/tls"
	"io"
	"log"
	"os"
)

func main() {
	cert, err := tls.LoadX509KeyPair(os.Args[1], os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	ln, err := tls.Listen("tcp", os.Args[3], &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}()
	}
}
EOF
(cd "$TMP/tls-echo-src" && go mod init tls-echo >/dev/null 2>&1 && go build -o "$TMP/tls-echo" .)
"$TMP/tls-echo" "$TMP/tls.crt" "$TMP/tls.key" "127.0.0.1:$TLS_PORT" > "$TMP/tls-echo.log" 2>&1 &
TLS_PID=$!

# Core agents for the three nodes (all online): the entry agent also runs the
# dispatched probe; the relay node's core agent is what makes the relay node
# online for path health (the data plane itself is the standalone role below).
start_agent() { # FILE OUT
  "$TMP/ferry-agent" -config "$1" > "$2" 2>&1 &
}
cat > "$TMP/agent-entry.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-entry",
  "token": "$ENTRY_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "role": "entry", "direction": "in", "bw_up_mbps": 10, "bw_down_mbps": 10 }
}
EOF
start_agent "$TMP/agent-entry.json" "$TMP/agent-entry.log"
ENTRY_PID=$!
expect "entry agent 上线" "1" "$(poll_health 1)"

cat > "$TMP/agent-relay.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-relay",
  "token": "$RELAY_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 },
  "relay": {
    "listen": "127.0.0.1:$RELAY_PORT",
    "landing_addr": "127.0.0.1:$TLS_PORT",
    "tunnel": "tls-camo",
    "server_name": "127.0.0.1",
    "ca_file": "$TMP/tls.crt"
  }
}
EOF
# Core mode for this node (ignores the relay block), then the relay role
# standalone on the same config (it never dials the panel).
start_agent "$TMP/agent-relay.json" "$TMP/agent-relay.log"
RELAY_CORE_PID=$!
expect "relay 节点 core agent 上线" "2" "$(poll_health 2)"

cat > "$TMP/agent-landing.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-landing",
  "token": "$LANDING_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 }
}
EOF
start_agent "$TMP/agent-landing.json" "$TMP/agent-landing.log"
LANDING_PID=$!
expect "landing agent 上线" "3" "$(poll_health 3)"

"$TMP/ferry-agent" -config "$TMP/agent-relay.json" -role relay > "$TMP/relay-role.log" 2>&1 &
RELAY_PID=$!
up=""
for i in $(seq 1 30); do
  up=$(python3 - "$RELAY_PORT" <<'PY' 2>/dev/null || true
import socket, sys
try:
    s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.5); s.close(); print("1")
except Exception:
    pass
PY
)
  [ "$up" = "1" ] && break
  sleep 0.2
done
expect "relay 前端监听就绪" "1" "$up"

log "F-2044: TCP 字节往返穿过真实 relay（front → tls-camo → TLS echo）..."
python3 - "$RELAY_PORT" <<'PY'
import socket, sys
s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=5)
s.settimeout(5)
s.sendall(b"relay-roundtrip")
data = b""
while len(data) < len(b"relay-roundtrip"):
    chunk = s.recv(128)
    if not chunk:
        break
    data += chunk
s.close()
assert data == b"relay-roundtrip", (data, b"relay-roundtrip")
PY
log "ok - relay TCP 回显字节一致"

log "F-2045: 建三段式 Path 并下发探测 ..."
code=$(curl -s -o "$TMP/path.json" -w '%{http_code}' -X POST "$BASE/api/paths" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"e2e-relay-path\",\"entry_node_id\":$ENTRY_ID,\"relay_node_id\":$RELAY_ID,\"landing_node_id\":$LANDING_ID,\"transport\":\"tls-camo\",\"direction\":\"out\"}")
expect "创建 entry→relay→landing Path" 201 "$code"
PATH_ID=$(json_get id "$TMP/path.json")

code=$(curl -s -o "$TMP/probe-task.json" -w '%{http_code}' -X PUT "$BASE/api/nodes/$ENTRY_ID/probe_tasks" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"specs":[{"name":"e2e-landing-probe","target_kind":"tunnel","target":"127.0.0.1:'"$TLS_PORT"'","interval_sec":5,"proto":"tcp"}]}')
expect "下发 TCP 探测任务" 200 "$code"
expect "任务已送达在线 agent" "true" "$(json_get delivered "$TMP/probe-task.json")"

# Poll until a report for the landing target lands: reachable + healthy +
# target_kind=tunnel + target_node_id=landing (address resolution at ingest).
# The first probe carries the F-0926 anti-storm jitter (up to 45s), so the
# window must comfortably exceed jitter + interval.
ok=""
for i in $(seq 1 150); do
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/api/probe-reports?node_id=$ENTRY_ID&limit=20" -o "$TMP/probe-reports.json"
  ok=$(TARGET_HOST="127.0.0.1:$TLS_PORT" TARGET_NODE="$LANDING_ID" python3 - "$TMP/probe-reports.json" <<'PY' 2>/dev/null || true
import json, os, sys
rows = json.load(open(sys.argv[1]))
host = os.environ["TARGET_HOST"]
want = int(os.environ["TARGET_NODE"])
ok = [r for r in rows if r.get("target_host") == host and r.get("reachable")
      and r.get("verdict") == "healthy" and r.get("target_kind") == "tunnel"
      and r.get("target_node_id") == want]
print("1" if ok else "")
PY
)
  [ "$ok" = "1" ] && break
  sleep 0.5
done
expect "探测报告落库（reachable/healthy/tunnel/目标节点解析）" "1" "$ok"

# Path health: all three endpoints online + fresh healthy landing verdict = healthy.
health=""
for i in $(seq 1 30); do
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/api/paths/$PATH_ID/health" -o "$TMP/path-health.json"
  health=$(json_get health "$TMP/path-health.json")
  [ "$health" = "healthy" ] && break
  sleep 0.5
done
expect "三段式 Path 健康=healthy" "healthy" "$health"

wait_health() { # WANT TRIES
  local want="$1" tries="$2" got=""
  for i in $(seq 1 "$tries"); do
    curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
      "$BASE/api/paths/$PATH_ID/health" -o "$TMP/path-health.json"
    got=$(json_get health "$TMP/path-health.json")
    [ "$got" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$got"
}

log "F-2050: landing TLS 服务故障 → 探测转 sick → Path 转 degraded ..."
kill "$TLS_PID" 2>/dev/null || true
TLS_PID=""
expect "探测故障后 Path=degraded" "degraded" "$(wait_health degraded 60)"

log "F-2051: landing TLS 服务恢复 → Path 回 healthy（重新可用）..."
"$TMP/tls-echo" "$TMP/tls.crt" "$TMP/tls.key" "127.0.0.1:$TLS_PORT" >> "$TMP/tls-echo.log" 2>&1 &
TLS_PID=$!
expect "服务恢复后 Path=healthy" "healthy" "$(wait_health healthy 60)"

log "F-2050: landing agent 宕机 → 节点 offline → Path=degraded ..."
kill "$LANDING_PID" 2>/dev/null || true
LANDING_PID=""
expect "landing 离线（agents_online=2）" "2" "$(poll_health 2)"
expect "节点故障后 Path=degraded" "degraded" "$(wait_health degraded 60)"

log "F-2051: landing agent 重连 → 节点回池 → Path=healthy ..."
"$TMP/ferry-agent" -config "$TMP/agent-landing.json" >> "$TMP/agent-landing.log" 2>&1 &
LANDING_PID=$!
expect "landing 重连（agents_online=3）" "3" "$(poll_health 3)"
expect "节点恢复后 Path=healthy" "healthy" "$(wait_health healthy 60)"

log "F-2072: relay 进程故障 → 数据面断（front listener 消失，隧道不可用）..."
kill "$RELAY_PID" 2>/dev/null || true
RELAY_PID=""
down=""
for i in $(seq 1 30); do
  down=$(python3 - "$RELAY_PORT" <<'PY' 2>/dev/null || true
import socket, sys
try:
    s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.5)
    s.close()
except Exception:
    print("1")
PY
)
  [ "$down" = "1" ] && break
  sleep 0.2
done
expect "relay 前端监听已消失" "1" "$down"

log "F-2072: relay 进程重启 → 数据面恢复（隧道重通）..."
"$TMP/ferry-agent" -config "$TMP/agent-relay.json" -role relay >> "$TMP/relay-role.log" 2>&1 &
RELAY_PID=$!
up=""
for i in $(seq 1 30); do
  up=$(python3 - "$RELAY_PORT" <<'PY' 2>/dev/null || true
import socket, sys
try:
    s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=0.5); s.close(); print("1")
except Exception:
    pass
PY
)
  [ "$up" = "1" ] && break
  sleep 0.2
done
expect "relay 前端监听恢复" "1" "$up"
python3 - "$RELAY_PORT" <<'PY'
import socket, sys
s = socket.create_connection(("127.0.0.1", int(sys.argv[1])), timeout=5)
s.settimeout(5)
s.sendall(b"relay-recover")
data = b""
while len(data) < len(b"relay-recover"):
    chunk = s.recv(128)
    if not chunk:
        break
    data += chunk
s.close()
assert data == b"relay-recover", (data, b"relay-recover")
PY
log "ok - relay 重启后 TCP 回显字节一致（数据面重通）"

log "F-2072: relay 节点 core agent 宕机 → relay 节点 offline → Path=degraded ..."
kill "$RELAY_CORE_PID" 2>/dev/null || true
RELAY_CORE_PID=""
expect "relay 节点离线（agents_online=2）" "2" "$(poll_health 2)"
expect "relay 节点故障后 Path=degraded" "degraded" "$(wait_health degraded 60)"

log "F-2072: relay 节点 core agent 重连 → Path=healthy ..."
"$TMP/ferry-agent" -config "$TMP/agent-relay.json" >> "$TMP/agent-relay.log" 2>&1 &
RELAY_CORE_PID=$!
expect "relay 节点重连（agents_online=3）" "3" "$(poll_health 3)"
expect "relay 节点恢复后 Path=healthy" "healthy" "$(wait_health healthy 60)"

log "PASS - F-2044 真实 relay 隧道 + F-2045 Path 与探测 + F-2050/2051 故障与恢复 + F-2072 relay 故障注入，全链路真二进制"