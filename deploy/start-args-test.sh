#!/usr/bin/env bash
# Regression for start.sh argument assembly (--profile must precede the compose subcommand):
# a fake docker on PATH only records argv and makes the readiness poll pass immediately, so the real
# start.sh runs end to end without touching a single real container. Four assertions: the postgres profile
# appears before up, the --profile=x equals form, no profile smuggled in when none is given, and an early
# failure with a DSN hint when the postgres profile lacks FERRY_PG_PASSWORD.
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
  *"exec -T ferry-server wget"*) exit 0 ;;                 # readiness poll passes immediately
  *"exec -T ferry-server sh -c"*) echo sqlite; exit 0 ;;   # driver lookup
  *"exec -T ferry-server test"*) exit 0 ;;                 # DB-file check
  *"port ferry-dash"*) echo 127.0.0.1:18080 ;;
  *"port ferry-panel"*) echo 127.0.0.1:18081 ;;
  *) exit 0 ;;
esac
EOF
chmod +x "$FAKEBIN/docker"

# Isolated layout: start.sh cds to the parent of its own deploy/, so the whole layout is copied into a temp
# directory and the repo root's .env is never touched.
SAVEDIR="$TMP/repo/deploy"
mkdir -p "$SAVEDIR"
cp deploy/start.sh deploy/docker-compose.yml "$SAVEDIR/"
cp .env.example "$TMP/repo/"

# The .env carries the postgres password, so the postgres profile preflight passes
printf 'FERRY_PG_PASSWORD=pg-test-pw\nFERRY_HTTP_PORT=18080\n' > "$TMP/repo/.env"

run_start() { # $1=output log; the rest are start.sh arguments (returns its exit code)
  local out="$1"
  shift
  : > "$TMP/argv.log"   # re-record every run so a previous round's argv is never read
  FAKE_LOG="$TMP/argv.log" PATH="$FAKEBIN:$PATH" bash "$SAVEDIR/start.sh" "$@" \
    > "$out" 2>&1
}
up_line() { grep -m1 ' up ' "$TMP/argv.log"; }
offset_of() { # $1=needle $2=string; returns the byte offset of the needle in the string
  printf '%s' "$2" | grep -boF -e "$1" | head -1 | cut -d: -f1 || true
}

# 1. --profile postgres must come before up
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

# 2. The --profile=postgres equals form must also land before the subcommand
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

# 3. No profile given means no --profile is smuggled in
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

# 4. A postgres profile missing FERRY_PG_PASSWORD must fail early and print the DSN hint
rm -f "$TMP/repo/.env"   # start.sh will generate it from .env.example (the password line there is commented out)
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
