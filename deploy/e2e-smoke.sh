#!/usr/bin/env bash
# 端到端部署链路冒烟（FERRY-004 最小骨架）：真起 ferry-server 二进制（sqlite
# 落临时目录）+ 真 ferry-agent 二进制拨真实 WebSocket，一条链路走通
# 健康检查/库初始化 → 管理员首登与鉴权 → 签发一次性注册凭据 → 注册建节点
# → agent 握手上线（健康计数+1）→ agent 断线回落 → 令牌轮换/兼容窗口/吊销
# → 配置下发与 agent 应答记账、面板改节点状态可见 → 备份后恢复再登录验证
# 节点数据。全程不 mock，数据只落临时目录；任一步失败打印全部服务日志尾部
# 后非零退出。CI 见 test.yml deploy-scripts job；本地需 go 工具链。
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${E2E_PORT:-18099}
TMP=$(mktemp -d)
SERVER_PID="" AGENT_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ -n "$AGENT_PID" ] && kill "$AGENT_PID" 2>/dev/null || true
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[e2e] $*"; }
expect() { # $1=描述 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
dump() {
  for f in server.log server2.log agent.log agent-old.log agent-new.log agent-revoked.log restore.log; do
    if [ -f "$TMP/$f" ]; then
      echo "--- $f 尾部 ---" >&2
      tail -n 40 "$TMP/$f" >&2 || true
    fi
  done
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

# expect_stays_zero DESC：~10 秒内 agents_online 不得变 1（用于「被拒的 agent
# 反复重试也上不来」的否定断言；中途变 1 即失败）
expect_stays_zero() {
  local i n
  for i in $(seq 1 20); do
    curl -s "$BASE/api/health" -o "$TMP/health-poll.json" 2>/dev/null || true
    n=$(agents_online_count "$TMP/health-poll.json")
    if [ "$n" != "0" ] && [ -n "$n" ]; then
      echo "FAIL - $1：agents_online=$n（不该上线却上线）" >&2
      dump; exit 1
    fi
    sleep 0.5
  done
  log "ok - $1"
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
FERRY_HEARTBEAT_SEC=5 \
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
# stub 内核：真 OS 进程（起后常驻并写痕迹），给 agent 的 procs 监管一个
# 可控被管对象。FERRY-004 的「被管内核二进制」外部件指真实隧道流量与
# 协议行为，进程监管面（配置→拉起→心跳落库→崩溃自拉）可本地等价收口。
cat > "$TMP/stub-kernel" <<EOF
#!/bin/sh
echo "up \$(date +%s)" >> "$TMP/kernel-runs.log"
sleep 600
EOF
chmod +x "$TMP/stub-kernel"
: > "$TMP/kernel-runs.log"
: > "$TMP/k1.json"
cat > "$TMP/agent.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-agent",
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

# proc_field FIELD：从 /api/nodes/:id/procs 取被管进程 k1 的字段（jq→python3 两级）
proc_field() {
  local f="$1" v=""
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/api/nodes/$NODE_ID/procs" -o "$TMP/procs.json"
  if command -v jq >/dev/null 2>&1; then
    v=$(jq -r --arg p k1 --arg k "$f" '.[] | select(.proc==$p) | .[$k]' \
      "$TMP/procs.json" 2>/dev/null) || v=""
  elif command -v python3 >/dev/null 2>&1; then
    v=$(PROC_FIELD="$f" python3 -c 'import json,os,sys
try:
    for r in json.load(open(sys.argv[1])):
        if r.get("proc") == "k1":
            print(r.get(os.environ["PROC_FIELD"], ""))
except Exception:
    pass' "$TMP/procs.json" 2>/dev/null) || v=""
  fi
  printf '%s' "$v"
}
log "被管内核进程监管（stub 真进程全链路）..."
NODE_ID=$(json_get id "$TMP/enroll.json")
state=""
for i in $(seq 1 40); do
  state=$(proc_field state)
  [ "$state" = "running" ] && break
  sleep 0.5
done
expect "内核进程心跳落库 state=running" "running" "$state"
expect "stub 内核真被拉起（启动痕迹）" "1" \
  "$([ "$(grep -c '^up ' "$TMP/kernel-runs.log" 2>/dev/null)" -ge 1 ] && echo 1 || echo 0)"
KPID=$(proc_field pid)
if ! kill "$KPID" 2>"$TMP/kill.err"; then
  echo "FAIL - 无法终止内核进程 pid=[$KPID]（$(cat "$TMP/kill.err" 2>/dev/null)）" >&2
  dump; exit 1
fi
state=""; rst=0
for i in $(seq 1 50); do
  state=$(proc_field state); rst=$(proc_field restarts)
  [ "$state" = "running" ] && [ "${rst:-0}" -ge 1 ] 2>/dev/null && break
  sleep 0.5
done
expect "内核崩溃自拉 state 回 running" "running" "$state"
expect "自拉后重启计数≥1" "1" "$([ "${rst:-0}" -ge 1 ] 2>/dev/null && echo 1 || echo 0)"
expect "stub 内核真被重新拉起（第二段痕迹）" "1" \
  "$([ "$(grep -c '^up ' "$TMP/kernel-runs.log" 2>/dev/null)" -ge 2 ] && echo 1 || echo 0)"
log "断线回落 ..."
kill "$AGENT_PID"; AGENT_PID=""
expect "agent 断线后计数回落" "0" "$(poll_health 0)"

log "令牌轮换 → 兼容窗口 → 吊销（C-06 全链路）..."
NODE_ID=$(json_get id "$TMP/enroll.json")
code=$(curl -s -o "$TMP/rotate.json" -w '%{http_code}' -X POST "$BASE/api/nodes/$NODE_ID/token" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "轮换令牌成功" 200 "$code"
NEW_TOKEN=$(json_get token "$TMP/rotate.json")
[ -n "$NEW_TOKEN" ] && [ "$NEW_TOKEN" != "$NODE_TOKEN" ] \
  || { echo "FAIL - 轮换响应缺新令牌或与旧令牌相同" >&2; dump; exit 1; }

# 兼容窗口：旧令牌在窗口内仍可握手上线
cp "$TMP/agent.json" "$TMP/agent-old.json"
"$TMP/ferry-agent" -config "$TMP/agent-old.json" > "$TMP/agent-old.log" 2>&1 &
AGENT_PID=$!
expect "兼容窗口内旧令牌可上线" "1" "$(poll_health 1)"
kill "$AGENT_PID"; AGENT_PID=""
expect "旧令牌 agent 断开" "0" "$(poll_health 0)"

# 吊销后：旧令牌立即拒绝（CRL）——agent 保持运行持续重试 ~10 秒，计数不得
# 变 1（起跑失败不算拒绝，要观察的是「反复重试也上不来」）
code=$(curl -s -o "$TMP/revoke.json" -w '%{http_code}' -X POST "$BASE/api/nodes/$NODE_ID/token/revoke" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"token\":\"$NODE_TOKEN\"}")
expect "吊销旧令牌成功" 200 "$code"
"$TMP/ferry-agent" -config "$TMP/agent-old.json" > "$TMP/agent-revoked.log" 2>&1 &
AGENT_PID=$!
expect_stays_zero "吊销后旧令牌被拒（重试 10 秒不上线）"
kill "$AGENT_PID"; AGENT_PID=""

# 新令牌照常上线
cat > "$TMP/agent-new.json" <<EOF
{
  "panel_url": "ws://127.0.0.1:$PORT/agent/ws",
  "agent_id": "e2e-agent",
  "token": "$NEW_TOKEN",
  "heartbeat_sec": 5,
  "meta": { "bw_up_mbps": 10, "bw_down_mbps": 10 }
}
EOF
"$TMP/ferry-agent" -config "$TMP/agent-new.json" > "$TMP/agent-new.log" 2>&1 &
AGENT_PID=$!
expect "新令牌上线" "1" "$(poll_health 1)"

# 配置下发 → agent 应答 → 服务端状态更新（A-18）：proc 故意用面板不存在的
# 名字，agent 会回「unknown proc」——该报错串只可能来自 agent 侧执行，等于
# 证明了下行帧真过 WS 抵达 agent 并回执（agent 离线只会走到超时 pending）。
t0=$(date +%s)
code=$(curl -s -o "$TMP/push.json" -w '%{http_code}' -X POST "$BASE/api/nodes/$NODE_ID/config" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"proc":"e2e-probe","payload":"{}"}')
t1=$(date +%s)
expect "配置下发返回 200" 200 "$code"
PUSH_STATUS=$(json_get status "$TMP/push.json")
[ "$PUSH_STATUS" = applied ] || [ "$PUSH_STATUS" = failed ] \
  || { echo "FAIL - 配置下发无应答（status=$PUSH_STATUS，agent 未回 config.ack）" >&2; dump; exit 1; }
grep -Eq 'unknown proc|config_path' "$TMP/push.json" \
  || { echo "FAIL - 应答内容不来自 agent 侧执行：$(cat "$TMP/push.json")" >&2; dump; exit 1; }
[ $((t1 - t0)) -lt 30 ] \
  || { echo "FAIL - 配置下发耗时 $((t1 - t0)) 秒，疑似超时未应答" >&2; dump; exit 1; }
log "ok - 配置下发经 WS 抵达 agent 并回执（status=$PUSH_STATUS，$((t1 - t0)) 秒）"

# 面板改节点元数据 → 服务端状态更新可见（agent 保持在线，不受影响）
code=$(curl -s -o "$TMP/node-upd.json" -w '%{http_code}' -X PUT "$BASE/api/nodes/$NODE_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"e2e-node","address":"e2e.example.com","port":443,"protocol":"vless","region":"ap-e2e"}')
expect "面板更新节点元数据" 200 "$code"
code=$(curl -s -o "$TMP/node-get.json" -w '%{http_code}' "$BASE/api/nodes/$NODE_ID" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "读取节点状态" 200 "$code"
grep -q '"region":"ap-e2e"' "$TMP/node-get.json" \
  || { echo "FAIL - 节点元数据未更新：$(cat "$TMP/node-get.json")" >&2; dump; exit 1; }
log "ok - 节点元数据更新落库可见"

# 服务端状态更新：下发结果（含 agent 应答的错误串）已记账可查
code=$(curl -s -o "$TMP/nconfigs.json" -w '%{http_code}' "$BASE/api/nodes/$NODE_ID/configs" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "读取配置下发历史" 200 "$code"
grep -q '"proc":"e2e-probe"' "$TMP/nconfigs.json" \
  || { echo "FAIL - 下发记录未落库：$(cat "$TMP/nconfigs.json")" >&2; dump; exit 1; }
grep -q '"error":' "$TMP/nconfigs.json" \
  || { echo "FAIL - 下发记录缺 agent 应答错误串" >&2; dump; exit 1; }
log "ok - 服务端已记账 agent 应答（状态与错误串落库）"
expect "配置下发后 agent 仍在线" "1" "$(poll_health 1)"
kill "$AGENT_PID"; AGENT_PID=""
expect "新令牌 agent 断开" "0" "$(poll_health 0)"

log "备份 → 停机 → 恢复到新目录 → 恢复后登录与节点数据 ..."
code=$(curl -s -o "$TMP/after-node.enc" -w '%{http_code}' "$BASE/admin/backup/db" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "节点在库后手动备份（附件即档）" 200 "$code"
kill "$SERVER_PID"; wait "$SERVER_PID" 2>/dev/null || true; SERVER_PID=""
KEY=$(sed -n 's/^FERRY_SECRET_KEY=//p' "$TMP/keys.env")
[ -n "$KEY" ] || { echo "FAIL - keys.env 缺 FERRY_SECRET_KEY" >&2; dump; exit 1; }
mkdir -p "$TMP/restored"
if ! bash deploy/restore.sh --backup "$TMP/after-node.enc" --data-dir "$TMP/restored" \
    --key "$KEY" --ferry-bin "$TMP/ferry-server" > "$TMP/restore.log" 2>&1; then
  echo "FAIL - restore.sh 失败：" >&2; tail -n 30 "$TMP/restore.log" >&2; dump; exit 1
fi
[ -f "$TMP/restored/ferry.db" ] \
  || { echo "FAIL - 恢复目录无 ferry.db" >&2; tail -n 30 "$TMP/restore.log" >&2; dump; exit 1; }
log "ok - 解密+导入完成（restored/ferry.db 就位）"

# 恢复后的实例：同 keyfile 起服务；故意给错误首启密码——已有管理员必须被保留
wait_health() {
  for i in $(seq 1 40); do
    code=$(curl -s -o "$TMP/health.json" -w '%{http_code}' "$BASE/api/health" || true)
    [ "$code" = 200 ] && return 0
    sleep 0.5
  done
  return 1
}
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/restored/ferry.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=wrong-on-purpose-0 \
FERRY_KEYFILE="$TMP/keys.env" FERRY_BACKUP_DIR="$TMP/backup" \
  "$TMP/ferry-server" > "$TMP/server2.log" 2>&1 &
SERVER_PID=$!
wait_health || { echo "FAIL - 恢复实例健康不可达" >&2; dump; exit 1; }
log "ok - 恢复实例健康可达"
code=$(curl -s -o "$TMP/login2.json" -w '%{http_code}' -X POST "$BASE/admin/login" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"e2e-pass-0123456"}')
expect "恢复后原密码可登录（首启密码不覆盖已有管理员）" 200 "$code"
ADMIN_TOKEN=$(json_get token "$TMP/login2.json")
code=$(curl -s -o "$TMP/nodes.json" -w '%{http_code}' "$BASE/api/nodes" \
  -H "Authorization: Bearer $ADMIN_TOKEN")
expect "恢复后节点数据在" 200 "$code"
grep -q '"name":"e2e-node"' "$TMP/nodes.json" \
  || { echo "FAIL - 恢复库缺 e2e-node" >&2; dump; exit 1; }
log "ok - e2e-node 随库恢复"

log "PASS：部署→认证→注册→通信→断线→轮换吊销→配置下发应答→备份恢复链路全通"

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
