#!/usr/bin/env bash
# Equivalent regression of agent-install.sh against a real systemd inside a container (the local
# fallback for FERRY-004's on-real-machine acceptance): the PATH fake sandbox (install-rollback-test.sh)
# can only simulate systemctl/journalctl return values, while this uses a real PID 1 to verify
# daemon-reload/enable/restart semantics, the real collection of journal failure diagnostics, and real
# recovery after a .prev rollback; pipeline four then downgrades "needs a production panel" to a local
# equivalent — a real ferry-server binary issues a one-time credential, and the real script inside the
# container uses it through the real enroll API to exchange for a real node token.
# Prerequisites: docker + host cgroup v2 (the image can be overridden via FERRY_SYSTEMD_IMAGE);
# skips when docker is unavailable (the PATH fake sandbox still covers script logic); pipeline four
# additionally needs the go toolchain.
set -u
cd "$(dirname "$0")/.."

IMAGE="${FERRY_SYSTEMD_IMAGE:-jrei/systemd-ubuntu:22.04}"

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "skip: docker 不可用，systemd 等价回归跳过（install-rollback-test.sh 仍覆盖脚本逻辑）"
  exit 0
fi

pass=0 fail=0
ok() { pass=$((pass+1)); echo "ok - $1"; }
bad() { fail=$((fail+1)); echo "FAIL - $1" >&2; }
assert_eq() { # $1=description $2=expected $3=actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1：want [$2] got [$3]"; fi
}
assert_contains() { # $1=description $2=substring expected $3=full text
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1：缺 [$2]" >&2 ;; esac
}

# json_get FILE KEY: jq/python3 first, falling back to text extraction (same approach as e2e-smoke)
json_get() {
  local key="$1" file="$2" v=""
  if command -v jq >/dev/null 2>&1; then
    v=$(jq -r --arg k "$key" '.[$k] // empty' "$file" 2>/dev/null) || v=""
  elif command -v python3 >/dev/null 2>&1; then
    v=$(python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception:
    pass' "$key" "$file" 2>/dev/null) || v=""
  else
    v=$(grep -o "\"$key\":[[:space:]]*\"[^\"]*\"" "$file" 2>/dev/null | head -1 | cut -d'"' -f4)
  fi
  printf '%s' "$v"
}

SB=$(mktemp -d)
CTR="ferry-sysd-smoke-$$"
SRV_PID=""
trap 'docker rm -f "$CTR" >/dev/null 2>&1; [ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null; rm -rf "$SB"' EXIT
mkdir -p "$SB/bin" "$SB/etc"

# good-agent: a resident process genuinely started by systemd (it writes run traces, so is-active only reports active when truly running)
cat > "$SB/good-agent" <<'EOF'
#!/bin/sh
echo "run $(date +%s) pid $$" >> /sb/runs.log
exec sleep 600
EOF
# bad-agent: a broken shebang (interpreter does not exist) — ExecStart must fail at exec
# (status=203/EXEC, journal records Main process exited), so the bad process is never born.
# A "runs but exits instantly" process cannot fake the failure: when instant exits resonate with
# Restart=always/the polling period, wait can catch a transient active window and judge the failure a
# success (intermittent flake); stripping the exec bit cannot either — the script installs with
# install -m 0755, which adds the exec bit back.
cat > "$SB/bad-agent" <<'EOF'
#!/nonexistent-interpreter
exit 1
EOF
chmod +x "$SB/good-agent" "$SB/bad-agent"

# Start the systemd container: on a cgroup v2-only host, systemd 249 gets EPERM using v1 mounts,
# so the host cgroup2 must be bind-mounted in with the host namespace (the standard systemd-in-docker recipe)
docker run -d --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -v "$SB":/sb -v "$(pwd)/deploy":/work:ro --name "$CTR" "$IMAGE" >/dev/null
ready=""
for i in $(seq 1 60); do
  s=$(docker exec "$CTR" systemctl is-system-running 2>/dev/null | tr -d '\n')
  case "$s" in running|degraded) ready=1; break ;; esac
  sleep 1
done
if [ -z "$ready" ]; then
  bad "容器内 systemd 未就绪（镜像 $IMAGE，末态 [$s]）"
  echo "-----------------------------"
  echo "pass=$pass fail=$fail"
  exit 1
fi
ok "容器内 systemd 就绪（$s）"

run_install() { # $1=sandbox binary name (under /sb/); $2..=extra passthrough; OUT/RC receive the result
  local bin="$1"; shift
  OUT=$(docker exec "$CTR" bash /work/agent-install.sh \
    --panel https://panel.example.com --token smoke-node-token \
    --binary "/sb/$bin" --install /sb/bin/ferry-agent \
    --config /sb/etc/agent.json "$@" 2>&1)
  RC=$?
}
svc_state() { docker exec "$CTR" systemctl is-active ferry-agent 2>/dev/null | tr -d '\n'; }

# ---- 1. Real first install: a real unit in a real directory, real enable/restart semantics, the process really started ----
run_install good-agent
assert_eq "容器内首装退出码" 0 "$RC"
assert_eq "真实 systemd 服务 active" "active" "$(svc_state)"
assert_eq "真实 enable 生效" "enabled" \
  "$(docker exec "$CTR" systemctl is-enabled ferry-agent 2>/dev/null | tr -d '\n')"
assert_contains "unit 落真目录且 ExecStart 指安装位" "ExecStart=/sb/bin/ferry-agent -config /sb/etc/agent.json" \
  "$(docker exec "$CTR" cat /etc/systemd/system/ferry-agent.service)"
assert_eq "ExecStart 真跑二进制（运行痕迹）" "1" \
  "$([ "$(grep -c '^run ' "$SB/runs.log" 2>/dev/null)" -ge 1 ] && echo 1 || echo 0)"
assert_contains "配置落盘含节点令牌" '"token": "smoke-node-token"' \
  "$(docker exec "$CTR" cat /sb/etc/agent.json 2>/dev/null)"
assert_eq "成功后 .prev 清除" "0" "$([ -e "$SB/bin/ferry-agent.prev" ] && echo 1 || echo 0)"

# ---- 2. Real upgrade failure: journal really collects failure diagnostics, .prev rolls back, the service really recovers ----
docker exec "$CTR" sh -c ': > /sb/runs.log'
run_install bad-agent
assert_eq "升级失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_contains "失败信息提示已回滚" "已回滚" "$OUT"
assert_contains "journal 诊断头在场" "最近日志（失败诊断）" "$OUT"
assert_contains "真实 journal 记录坏进程退出" "Main process exited" "$OUT"
assert_eq "回滚后二进制还原旧件" "0" "$(cmp -s "$SB/bin/ferry-agent" "$SB/good-agent" && echo 0 || echo 1)"
assert_eq "回滚后服务恢复 active" "active" "$(svc_state)"
assert_contains "恢复后 ExecStart 真跑旧件（good 痕迹非 crash 痕迹）" "1" \
  "$([ "$(grep -c '^run ' "$SB/runs.log" 2>/dev/null)" -ge 1 ] && echo 1 || echo 0)"
assert_eq ".prev 保留供手动核对（内容为升级前旧件）" "0" "$(cmp -s "$SB/bin/ferry-agent.prev" "$SB/good-agent" && echo 0 || echo 1)"

# ---- 3. Real first-install failure cleanup: with no previous binary, the unit and binary are removed and the service is no longer active ----
docker exec "$CTR" rm -f /sb/bin/ferry-agent /sb/bin/ferry-agent.prev
docker exec "$CTR" sh -c ': > /sb/runs.log'
run_install bad-agent
assert_eq "首装失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_contains "首装失败提示已清理" "已清理本次安装产物" "$OUT"
assert_eq "失败后 unit 已清理" "0" "$(docker exec "$CTR" test -e /etc/systemd/system/ferry-agent.service && echo 1 || echo 0)"
assert_eq "失败后二进制已清理" "0" "$([ -e "$SB/bin/ferry-agent" ] && echo 1 || echo 0)"
st=$(svc_state); [ "$st" = "active" ] && bad "清理后服务仍 active" || ok "清理后服务不再 active（$st）"

# ---- 4. Real panel registration path: the real script in the container goes through the real enroll API against a real ferry-server ----
# The local equivalent of "needs a production panel": the ferry-server binary IS the panel. The script -> panel
# enroll contract previously existed only as two half-pictures (a sandbox fake curl serving 201, and e2e calling
# the API directly); this stitches both halves into a fully real one: a real server issues a one-time credential
# -> the real script inside the container registers with it and exchanges it for a real node token -> the token
# lands in the config, the panel's node row is queryable -> the credential's one-time property holds against the
# real script too.
# Needs the go toolchain to build the server; without go this path is skipped (the first three still cover script behavior).
SRV_PORT="${FERRY_SMOKE_PORT:-18101}"
mkdir -p "$SB/server"
node_field() { # $1=admin token $2=node name $3=field: read that node's field from /api/nodes
  local body
  body=$(curl -s -H "Authorization: Bearer $1" "http://127.0.0.1:$SRV_PORT/api/nodes")
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$body" | jq -r --arg n "$2" --arg k "$3" '.[] | select(.name==$n) | .[$k]' 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    printf '%s' "$body" | NODE_NAME="$2" NODE_KEY="$3" python3 -c 'import json,os,sys
for n in json.load(sys.stdin):
    if n.get("name") == os.environ["NODE_NAME"]:
        print(n.get(os.environ["NODE_KEY"], ""))' 2>/dev/null
  fi
}
if ! command -v go >/dev/null 2>&1; then
  ok "跳过真面板注册链路（无 go 工具链，前三链路仍覆盖脚本行为）"
else
  echo "[smoke] 构建 ferry-server（真面板注册链路）..."
  if ! (cd server && go build -o "$SB/server/ferry-server" ./cmd/ferry) 2>"$SB/server/build.log"; then
    bad "ferry-server 构建失败"
    tail -5 "$SB/server/build.log" >&2
  else
    FERRY_ADDR="0.0.0.0:$SRV_PORT" \
    FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$SB/server/panel.db" \
    FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=smoke-pass-0123456 \
    FERRY_KEYFILE="$SB/server/keys.env" \
    FERRY_BACKUP_DIR="$SB/server/backup" FERRY_BACKUP_CRON="@every 24h" \
      "$SB/server/ferry-server" > "$SB/server/server.log" 2>&1 &
    SRV_PID=$!
    code=""
    for i in $(seq 1 40); do
      code=$(curl -s -o "$SB/server/health.json" -w '%{http_code}' \
        "http://127.0.0.1:$SRV_PORT/api/health" 2>/dev/null || true)
      [ "$code" = 200 ] && break
      sleep 0.5
    done
    assert_eq "真面板健康检查可达" 200 "$code"
    code=$(curl -s -o "$SB/server/login.json" -w '%{http_code}' -X POST \
      "http://127.0.0.1:$SRV_PORT/admin/login" -H 'Content-Type: application/json' \
      -d '{"username":"admin","password":"smoke-pass-0123456"}')
    assert_eq "管理员登录成功" 200 "$code"
    ADMIN_TOKEN=$(json_get token "$SB/server/login.json")
    code=$(curl -s -o "$SB/server/cred.json" -w '%{http_code}' -X POST \
      "http://127.0.0.1:$SRV_PORT/api/enrollment-tokens" \
      -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
      -d '{"note":"systemd-smoke"}')
    assert_eq "签发一次性注册凭据" 201 "$code"
    CRED=$(json_get token "$SB/server/cred.json")
    # Container -> host goes through the default bridge gateway
    GW=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null)
    GW=${GW:-172.17.0.1}
    # The image ships without curl, which the script's download/registration both need: install it before the registration path
    echo "[smoke] 容器内补装 curl（注册链路依赖，镜像不带）..."
    docker exec "$CTR" sh -c 'command -v curl >/dev/null 2>&1 || (apt-get update -qq && apt-get install -y -qq curl) >/dev/null 2>&1'
    if ! docker exec "$CTR" sh -c 'command -v curl >/dev/null 2>&1'; then
      bad "容器内 curl 补装失败，真面板注册链路无法执行"
    else
    OUT=$(docker exec "$CTR" bash /work/agent-install.sh \
      --panel "http://$GW:$SRV_PORT" --register panel-node \
      --enroll-token "$CRED" --address panel-node.example.com \
      --binary /sb/good-agent --install /sb/bin/ferry-agent \
      --config /sb/etc/agent-panel.json 2>&1)
    RC=$?
    assert_eq "真面板注册链路安装退出码" 0 "$RC"
    assert_eq "注册链路服务 active" "active" "$(svc_state)"
    docker exec "$CTR" cat /sb/etc/agent-panel.json > "$SB/cfg4.json"
    SRV_TOKEN=$(node_field "$ADMIN_TOKEN" panel-node token)
    assert_eq "面板节点行已建（凭据消费）" "1" "$([ -n "$SRV_TOKEN" ] && echo 1 || echo 0)"
    assert_eq "配置令牌=真面板签发的节点令牌" "$SRV_TOKEN" "$(json_get token "$SB/cfg4.json")"
    assert_eq "配置令牌非注册凭据本身" "1" \
      "$([ "$(json_get token "$SB/cfg4.json")" != "$CRED" ] && echo 1 || echo 0)"
    # The credential's one-time property holds against the real script: re-registering with the same credential must be
    # rejected by the panel with 401, and the rejection must happen before any file is touched — the existing install stays intact
    OUT=$(docker exec "$CTR" bash /work/agent-install.sh \
      --panel "http://$GW:$SRV_PORT" --register panel-node-replay \
      --enroll-token "$CRED" --address replay.example.com \
      --binary /sb/good-agent --install /sb/bin/ferry-agent \
      --config /sb/etc/agent-panel.json 2>&1)
    RC=$?
    assert_eq "凭据重放经真脚本退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
    assert_eq "重放后服务仍 active" "active" "$(svc_state)"
    docker exec "$CTR" cat /sb/etc/agent-panel.json > "$SB/cfg4b.json"
    assert_eq "重放后配置原样" "$SRV_TOKEN" "$(json_get token "$SB/cfg4b.json")"
    assert_eq "重放未在面板建节点" "" "$(node_field "$ADMIN_TOKEN" panel-node-replay id)"
    fi
  fi
fi

echo "-----------------------------"
echo "pass=$pass fail=$fail"
if [ "$fail" != 0 ]; then
  [ -n "${OUT:-}" ] && { echo "--- 安装脚本输出尾部 ---" >&2; printf '%s\n' "$OUT" | tail -n 20 >&2; }
  [ -f "$SB/server/server.log" ] && { echo "--- server 日志尾部 ---" >&2; tail -n 40 "$SB/server/server.log" >&2; }
fi
[ "$fail" = 0 ]
