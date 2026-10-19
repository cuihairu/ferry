#!/usr/bin/env bash
# start.sh 参数装配回归（--profile 必须排在 compose 子命令之前）：
# PATH 假 docker 只记录 argv 并让就绪轮询立即通过，真 start.sh 全流程跑完
# 但一个真容器都不碰。四条断言：postgres profile 出现在 up 之前、
# --profile=x 等号写法、无 profile 时不夹带、postgres profile 缺
# FERRY_PG_PASSWORD 时早失败并打印 DSN 提示。
set -euo pipefail
cd "$(dirname "$0")/.."

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
PASS=0
ok() { echo "ok - $*"; PASS=$((PASS + 1)); }
fail() { echo "FAIL - $*" >&2; exit 1; }

FAKEBIN="$TMP/fakebin"
mkdir -p "$FAKEBIN"
cat > "$FAKEBIN/docker" <<'EOF'
#!/bin/sh
echo "$*" >> "$FAKE_LOG"
case "$*" in
  *"exec -T ferry-server wget"*) exit 0 ;;                 # 健康轮询立即通过
  *"exec -T ferry-server sh -c"*) echo sqlite; exit 0 ;;   # 驱动口径
  *"exec -T ferry-server test"*) exit 0 ;;                 # 建库校验
  *"port ferry-dash"*) echo 127.0.0.1:18080 ;;
  *"port ferry-panel"*) echo 127.0.0.1:18081 ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$FAKEBIN/docker"

# 隔离布局：start.sh 会 cd 到自身 deploy/ 的上级，故整套布局拷进临时目录，
# 绝不动仓库根的 .env。
SAVEDIR="$TMP/repo/deploy"
mkdir -p "$SAVEDIR"
cp deploy/start.sh deploy/docker-compose.yml "$SAVEDIR/"
cp .env.example "$TMP/repo/"

# .env 里给了 postgres 密码：postgres profile 预检通过
printf 'FERRY_PG_PASSWORD=pg-test-pw\nFERRY_HTTP_PORT=18080\n' > "$TMP/repo/.env"

run_start() { # $1=输出日志；其余为 start.sh 参数（返回其退出码）
  local out="$1"
  shift
  : > "$TMP/argv.log"   # 每次重记，避免读到上一轮 argv
  FAKE_LOG="$TMP/argv.log" PATH="$FAKEBIN:$PATH" bash "$SAVEDIR/start.sh" "$@" \
    > "$out" 2>&1
}
up_line() { grep -m1 ' up ' "$TMP/argv.log"; }
offset_of() { # $1=片段 $2=字符串；返回片段在字符串中的字节偏移
  printf '%s' "$2" | grep -boF -e "$1" | head -1 | cut -d: -f1 || true
}

# 1. --profile postgres 必须排在 up 之前
set +e
run_start "$TMP/1.log" --profile postgres
RC=$?
set -e
[ "$RC" = 0 ] && ok "--profile postgres 全流程退出 0" || fail "--profile postgres 退出 $RC：$(tail -5 "$TMP/1.log")"
LINE=$(up_line)
P=$(offset_of '--profile postgres' "$LINE")
U=$(offset_of ' up ' "$LINE")
[ -n "$P" ] && [ -n "$U" ] && [ "$P" -lt "$U" ] \
  && ok "--profile 排在子命令 up 之前（$P < $U）" \
  || fail "--profile 位置错误：profile@$P up@$U 行=[$LINE]"

# 2. --profile=postgres 等号写法同样落到前段
set +e
run_start "$TMP/2.log" --profile=postgres
RC=$?
set -e
[ "$RC" = 0 ] && ok "--profile=postgres 退出 0" || fail "--profile=postgres 退出 $RC"
LINE=$(up_line)
P=$(offset_of '--profile postgres' "$LINE")
U=$(offset_of ' up ' "$LINE")
[ -n "$P" ] && [ -n "$U" ] && [ "$P" -lt "$U" ] \
  && ok "等号写法同样排在 up 之前（$P < $U）" \
  || fail "等号写法位置错误：profile@$P up@$U 行=[$LINE]"

# 3. 不带 profile 时不夹带任何 --profile
set +e
run_start "$TMP/3.log"
RC=$?
set -e
[ "$RC" = 0 ] && ok "无 profile 全流程退出 0" || fail "无 profile 退出 $RC"
LINE=$(up_line)
case "$LINE" in
  *--profile*) fail "无 profile 却夹带 --profile：$LINE" ;;
  *) ok "无 profile 时 compose 命令不夹带 --profile" ;;
esac

# 4. postgres profile 缺 FERRY_PG_PASSWORD 必须早失败并给出 DSN 提示
rm -f "$TMP/repo/.env"   # start.sh 会从 .env.example 生成（密码行是注释）
set +e
run_start "$TMP/4.log" --profile postgres
RC=$?
set -e
[ "$RC" != 0 ] && ok "缺 FERRY_PG_PASSWORD 早失败（退出 $RC）" \
  || fail "缺 FERRY_PG_PASSWORD 未失败，postgres 会静默拒启"
grep -q 'FERRY_PG_PASSWORD' "$TMP/4.log" \
  && grep -q 'ferry-postgres:5432/ferry' "$TMP/4.log" \
  && ok "失败信息给出密码项与 DSN 口径" \
  || fail "失败信息缺少提示：$(cat "$TMP/4.log")"

echo "全部通过（$PASS 断言）"
