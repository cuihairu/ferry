#!/usr/bin/env bash
# agent-install.sh 在容器内真实 systemd 的等价回归（FERRY-004 真机验收的本地
# 降级面）：PATH 假件沙箱（install-rollback-test.sh）只能模拟 systemctl/
# journalctl 的返回值，这里用真实 PID 1 验证 daemon-reload/enable/restart
# 语义、journal 失败诊断的真实采集与 .prev 回滚后的真实恢复；链路四再把
# 「需生产真面板」降级为本地等价——真 ferry-server 二进制签发一次性凭据，
# 容器内真脚本凭它走真 enroll API 换真节点令牌。
# 前置：docker + 宿主 cgroup v2（镜像可经 FERRY_SYSTEMD_IMAGE 覆盖）；
# 无 docker 时跳过（PATH 假件沙箱仍覆盖脚本逻辑）；链路四另需 go 工具链。
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
assert_eq() { # $1=描述 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1：want [$2] got [$3]"; fi
}
assert_contains() { # $1=描述 $2=应含子串 $3=全文
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1：缺 [$2]" >&2 ;; esac
}

# json_get FILE KEY：优先 jq/python3，退回文本截取（与 e2e-smoke 同口径）
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

# good-agent：真被 systemd 拉起的常驻进程（写运行痕迹，is-active 才是 active）
cat > "$SB/good-agent" <<'EOF'
#!/bin/sh
echo "run $(date +%s) pid $$" >> /sb/runs.log
exec sleep 600
EOF
# bad-agent：坏 shebang（解释器不存在）——ExecStart 必 exec 失败
# （status=203/EXEC，journal 记 Main process exited），坏进程从不诞生。
# 不能用「可执行但秒退」造失败：秒退进程与 Restart=always/轮询周期共振时
# wait 会撞见瞬时 active 窗口把失败判成成功（间歇 flake）；也不能用去执行位
# 造——脚本安装时 install -m 0755 会把执行位加回来。
cat > "$SB/bad-agent" <<'EOF'
#!/nonexistent-interpreter
exit 1
EOF
chmod +x "$SB/good-agent" "$SB/bad-agent"

# 起 systemd 容器：cgroup v2-only 宿主上 systemd 249 走 v1 挂载会 EPERM，
# 需把宿主 cgroup2 绑进容器并 host 命名空间（systemd-in-docker 标准配方）
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

run_install() { # $1=沙箱二进制名（/sb/ 下）；$2..=额外透传；OUT/RC 承接结果
  local bin="$1"; shift
  OUT=$(docker exec "$CTR" bash /work/agent-install.sh \
    --panel https://panel.example.com --token smoke-node-token \
    --binary "/sb/$bin" --install /sb/bin/ferry-agent \
    --config /sb/etc/agent.json "$@" 2>&1)
  RC=$?
}
svc_state() { docker exec "$CTR" systemctl is-active ferry-agent 2>/dev/null | tr -d '\n'; }

# ---- 1. 真实首装：真 unit 落真目录、enable/restart 真语义、进程真被拉起 ----
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

# ---- 2. 真实升级失败：journal 真实采集失败诊断、.prev 回滚、服务真恢复 ----
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

# ---- 3. 真实首装失败清理：无旧件时清掉 unit 与二进制，服务不再 active ----
docker exec "$CTR" rm -f /sb/bin/ferry-agent /sb/bin/ferry-agent.prev
docker exec "$CTR" sh -c ': > /sb/runs.log'
run_install bad-agent
assert_eq "首装失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_contains "首装失败提示已清理" "已清理本次安装产物" "$OUT"
assert_eq "失败后 unit 已清理" "0" "$(docker exec "$CTR" test -e /etc/systemd/system/ferry-agent.service && echo 1 || echo 0)"
assert_eq "失败后二进制已清理" "0" "$([ -e "$SB/bin/ferry-agent" ] && echo 1 || echo 0)"
st=$(svc_state); [ "$st" = "active" ] && bad "清理后服务仍 active" || ok "清理后服务不再 active（$st）"

# ---- 4. 真面板注册链路：容器内真脚本对真 ferry-server 走真 enroll API ----
# 「需生产真面板」的本地等价降级：ferry-server 二进制就是面板本体。脚本→
# 面板的 enroll 契约此前只有两个半面（沙箱假 curl 供 201、e2e 直调 API），
# 这里把两半拼成全真：真 server 签发一次性凭据 → 容器内真脚本凭它注册换
# 真节点令牌 → 令牌落配置、面板节点行可查 → 凭据一次性对真脚本同样成立。
# 需 go 工具链构建 server；无 go 跳过本链路（前三链路仍覆盖脚本行为）。
SRV_PORT="${FERRY_SMOKE_PORT:-18101}"
mkdir -p "$SB/server"
node_field() { # $1=admin token $2=节点名 $3=字段：从 /api/nodes 取该节点字段
  local body
  body=$(curl -s -H "Authorization: Bearer $1" "http://127.0.0.1:$SRV_PORT/api/nodes")
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$body" | jq -r --arg n "$2" --arg k "$3" '.[] | select(.name==$n) | .[$k]' 2>/dev/null
  elif command -v python3 >/dev/null 2>&1; then
    printf '%s' "$body" | NODE_NAME="$2" NODE_KEY="$3" python3 -c 'import json,os
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
    # 容器→宿主走默认网桥网关
    GW=$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null)
    GW=${GW:-172.17.0.1}
    # 镜像不带 curl，脚本的下载/注册都依赖它：装好再走注册链路
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
    # 凭据一次性对真脚本成立：同凭据再注册必须被面板 401 拒绝，
    # 且拒绝发生在动文件之前——既有安装原样无恙
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
