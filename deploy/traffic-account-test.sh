#!/usr/bin/env bash
# Traffic accounting accuracy smoke (F-2052): the server-side accounting boundary
# driven over the REAL wire protocol. The kernel (xray) is the FERRY-004 external
# concern (same scoping as every stub-kernel smoke), so the client here is a thin
# real protocol speaker: it imports the actual packages/agentproto types and talks
# gorilla/websocket exactly like agent/internal/link does -- nothing is invented.
# Flow: build ferry-server; enroll one entry-role node; create a panel user whose
# username matches the reported xray email; the client handshakes (hello ->
# hello_ack), reports one traffic frame with known deltas (id t-1), re-sends the
# IDENTICAL frame (dedupe must not double-count, ack still 200), then a new frame
# (id t-2) with different deltas; the flow-account audit view
# (GET /api/traffic-recon) must show user_billed and node_metered at exactly the
# reported byte sums (3000 then 4200), delta=0. Nothing mocked at the protocol
# layer; data lands only in a temp dir; failures print log tails and exit 1.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${TRAFFIC_PORT:-$(( (RANDOM % 200) + 18900 ))}
TMP=$(mktemp -d)
SERVER_PID="" CLIENT_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$CLIENT_PID" ] && kill "$CLIENT_PID" 2>/dev/null || true
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[traffic-acct] $*"; }
expect() {
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
dump() {
  for f in server.log client.log recon.json; do
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

log "构建 ferry-server ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)

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

log "签发凭据并注册 entry 节点 ..."
code=$(curl -s -o "$TMP/enrolltok.json" -w '%{http_code}' -X POST "$BASE/api/enrollment-tokens" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' -d '{"note":"acct"}')
expect "签发凭据" 201 "$code"
CRED=$(json_get token "$TMP/enrolltok.json")
code=$(curl -s -o "$TMP/enroll.json" -w '%{http_code}' -X POST "$BASE/api/enroll" \
  -H "Authorization: Bearer $CRED" -H 'Content-Type: application/json' \
  -d '{"name":"e2e-acct","address":"127.0.0.1","port":'"$PORT"',"protocol":"vless","meta":{"role":"entry","direction":"in"}}')
expect "注册 entry 节点" 201 "$code"
NODE_TOKEN=$(json_get token "$TMP/enroll.json")
NID=$(json_get id "$TMP/enroll.json")

log "创建面板用户（username = 上报 email）..."
code=$(curl -s -o "$TMP/user.json" -w '%{http_code}' -X POST "$BASE/api/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"username":"acc-user","quota_bytes":1073741824,"expires_at":"2030-01-01T00:00:00Z"}')
expect "创建用户 acc-user" 201 "$code"

log "构建协议客户端（真 agentproto + 真 gorilla/websocket）..."
mkdir -p "$TMP/acct-src"
cat > "$TMP/acct-src/go.mod" <<EOF
module acctclient

go 1.24

require (
	github.com/cuihairu/ferry/packages/agentproto v0.0.0
	github.com/gorilla/websocket v1.5.3
)

replace github.com/cuihairu/ferry/packages/agentproto => $(pwd)/packages/agentproto
EOF
cat > "$TMP/acct-src/main.go" <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

func main() {
	url, token, agentID := os.Args[1], os.Args[2], os.Args[3]
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)

	send := func(id, t string, payload any) {
		env, err := agentproto.NewEnvelope(id, t, payload)
		if err != nil {
			fmt.Println("envelope:", err)
			os.Exit(1)
		}
		raw, _ := env.Marshal()
		if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
			fmt.Println("write:", err)
			os.Exit(1)
		}
	}
	readType := func(want string) agentproto.Envelope {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				fmt.Println("read:", err)
				os.Exit(1)
			}
			var env agentproto.Envelope
			err = json.Unmarshal(raw, &env)
			if err != nil {
				fmt.Println("bad envelope:", err)
				os.Exit(1)
			}
			if env.Type == want {
				return env
			}
		}
	}

	// Meta must declare the entry role: user traffic is metered single-ended on
	// entry/both nodes (F-1242), so a hello without meta lands as landing (the
	// Normalize default) and the ledger rightly drops it.
	send("hello", agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: agentID, Version: "acct-e2e",
		StartedAt: time.Now(),
		Meta: agentproto.NodeMeta{
			Role: agentproto.RoleEntry, Direction: agentproto.DirectionIn,
			BwUpMbps: 10, BwDownMbps: 10,
		},
	})
	readType(agentproto.MsgHelloAck)
	fmt.Println("hello_ack ok")

	// frame t-1: user acc-user rx=1000 tx=2000, proc k1 rx=1000 tx=2000 conns=3
	frame := func(id string, rx, tx uint64) agentproto.TrafficReport {
		return agentproto.TrafficReport{Items: []agentproto.ProcTraffic{{
			Proc: "k1", Rx: rx, Tx: tx, Conns: 3, At: time.Now(),
			Users: []agentproto.UserTraffic{{Email: "acc-user", Rx: rx, Tx: tx}},
		}}}
	}
	send("t-1", agentproto.MsgTraffic, frame("t-1", 1000, 2000))
	ack := readType(agentproto.MsgTrafficAck)
	var a agentproto.TrafficAck
	if err := ack.Decode(&a); err != nil || a.Recorded != 1 {
		fmt.Printf("ack1: %+v err=%v\n", a, err)
		os.Exit(1)
	}
	fmt.Println("t-1 recorded=1")

	// identical frame id again: the server must dedupe BEFORE accounting and
	// still ack (FERRY-004) -- the ledger must not move.
	send("t-1", agentproto.MsgTraffic, frame("t-1", 1000, 2000))
	ack = readType(agentproto.MsgTrafficAck)
	if err := ack.Decode(&a); err != nil || a.Recorded != 1 {
		fmt.Printf("ack2: %+v err=%v\n", a, err)
		os.Exit(1)
	}
	fmt.Println("t-1 duplicate acked (dedupe expected server-side)")

	// new frame t-2: +500/+700
	send("t-2", agentproto.MsgTraffic, frame("t-2", 500, 700))
	ack = readType(agentproto.MsgTrafficAck)
	if err := ack.Decode(&a); err != nil || a.Recorded != 1 {
		fmt.Printf("ack3: %+v err=%v\n", a, err)
		os.Exit(1)
	}
	fmt.Println("t-2 recorded=1")
	fmt.Println("CLIENT-DONE")
}
EOF
(cd "$TMP/acct-src" && GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod go mod tidy > "$TMP/tidy.log" 2>&1 \
  && GOPROXY=off GOSUMDB=off go build -o "$TMP/acct-client" .) \
  || { echo "FAIL - 客户端构建失败" >&2; cat "$TMP/tidy.log" >&2 2>/dev/null; dump; exit 1; }

log "客户端握手 + 三帧上报 ..."
"$TMP/acct-client" "ws://127.0.0.1:$PORT/agent/ws" "$NODE_TOKEN" "e2e-acct" > "$TMP/client.log" 2>&1
grep -q 'CLIENT-DONE' "$TMP/client.log" || { echo "FAIL - 客户端未完成" >&2; dump; exit 1; }
grep -q 'hello_ack ok' "$TMP/client.log" && log "ok - hello 握手成功"
grep -q 't-1 recorded=1' "$TMP/client.log" && log "ok - 首帧记账 ack recorded=1"
grep -q 'duplicate acked' "$TMP/client.log" && log "ok - 重复帧 ack 正常返回"

wait_sum() { # WANT TRIES
  local want="$1" tries="$2" got=""
  for i in $(seq 1 "$tries"); do
    curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
      "$BASE/api/traffic-recon?days=1" -o "$TMP/recon.json"
    got=$(NID="$NID" python3 - "$TMP/recon.json" <<'PY' 2>/dev/null || true
import json, os, sys
try:
    rows = json.load(open(sys.argv[1]))["nodes"]
    r = [x for x in rows if x["node_id"] == int(os.environ["NID"])]
    print(r[0]["user_billed"] if r else "")
except Exception:
    pass
PY
)
    [ "$got" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$got"
}

log "断言台账逐字节准确 ..."
# All three frames are sent back-to-back, so the audit view is observed only at
# its final value: 3000+1200=4200. A double-counted duplicate would read 7200,
# so the exact sum still proves idempotency at the accounting boundary.
expect "逐字节合计 user_billed=4200（重复帧不双算）" "4200" "$(wait_sum 4200 20)"
expect "台账稳定（仍 4200）" "4200" "$(wait_sum 4200 5)"

NID="$NID" python3 - "$TMP/recon.json" <<'PY'
import json, os, sys
rows = json.load(open(sys.argv[1]))["nodes"]
r = [x for x in rows if x["node_id"] == int(os.environ["NID"])]
assert r, ("node row missing", rows)
r = r[0]
assert r["node_metered"] == 4200, ("node_metered", r)
assert r["delta"] == 0, ("delta", r)
assert r["status"] == "ok", ("status", r)
PY
log "ok - 节点台账 node_metered=4200，双台账 delta=0 status=ok"

log "PASS - F-2052 流量记账准确性（逐字节+重复帧幂等+单端计量），协议层无 mock"