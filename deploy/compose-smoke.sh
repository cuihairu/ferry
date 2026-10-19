#!/usr/bin/env bash
# compose 真部署冒烟（FERRY-004 收尾）：临时 compose project + 临时卷真跑
# docker compose build/up，经 Caddy 前端验证三件事——静态产物托管、
# history 路由回退、后端顶层前缀反代（漏登记的典型故障形态：GET 返回
# index.html(200)、POST 返回 405，能被本脚本的 POST 断言打回来）；
# 两条腿：sqlite 默认存储 + postgres profile 存储切换（compose 注释里
# 记载但从未被任何自动化跑过的路径）。结束 down -v 清容器与卷，
# 随机 project 名与真实部署互不干扰，绝不碰本机面板数据。
# 只放 nightly：build 是真编译（Go 前端两套 + 后端），不宜进 push 门禁。
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(pwd)

if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo "SKIPPED：未装 docker compose"
  exit 0
fi

TMP=$(mktemp -d)
LOGS="$TMP"
ADMIN_USER=admin
ADMIN_PW=$(printf 'compose-e2e-%032d' "$(date +%s%N | cut -c1-32)")
PG_PW=$(printf 'pg-e2e-%016d' "$(date +%s%N | cut -c1-16)")
die() { echo "FATAL $*" >&2; exit 1; }
free_port() { # [base, base+400] 内找未被占用的 TCP 端口（本机常已有面板占 18080）
  local base="$1" p
  for p in $(seq "$base" "$((base + 400))"); do
    (echo > "/dev/tcp/127.0.0.1/$p") >/dev/null 2>&1 && continue
    echo "$p"
    return 0
  done
  die "在 $base..$((base + 400)) 找不到空闲端口"
}
A_PORT=${E2E_HTTP_PORT:-$(free_port 18080)}
P_PORT=${E2E_PANEL_PORT:-$(free_port 19000)}
PROJECTS="" # 空格分隔 "project:envfile"，EXIT 时逐个 down -v
CLEANED=0

cleanup() {
  CLEANED=1
  for p in $PROJECTS; do
    n="${p%%:*}"; f="${p#*:}"
    docker compose --project-name "$n" --env-file "$f" -f "$ROOT/deploy/docker-compose.yml" \
      down -v --remove-orphans >/dev/null 2>&1 || true
  done
  rm -rf "$TMP"
}
trap cleanup EXIT

log() { echo "[compose] $*"; }
ok() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
bad() {
  printf '\033[31mFAIL\033[0m %s\n' "$*" >&2
  for f in "$LOGS"/*.log; do
    [ -f "$f" ] || continue
    echo "--- $f 尾部 ---" >&2
    tail -n 30 "$f" >&2 || true
  done
  exit 1
}
expect() { # $1=描述 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then ok "$1"
  else bad "$1：want [$2] got [$3]"; fi
}
json_get() { # $1=key $2=file
  local k="$1" f="$2" v=""
  if command -v jq >/dev/null 2>&1; then
    v=$(jq -r --arg k "$k" '.[$k] // empty' "$f" 2>/dev/null) || v=""
  elif command -v python3 >/dev/null 2>&1; then
    v=$(python3 -c 'import json,sys
try:
    print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception:
    pass' "$k" "$f" 2>/dev/null) || v=""
  else
    v=$(grep -o "\"$k\":[[:space:]]*\"[^\"]*\"" "$f" 2>/dev/null | head -1 | cut -d'"' -f4)
  fi
  printf '%s' "$v"
}
http_get() { # $1=url $2=outfile（返回状态码）
  curl -sS -o "$2" -w '%{http_code}' "$1"
}
http_post() { # $1=url $2=body $3=outfile（返回状态码）
  curl -sS -o "$3" -w '%{http_code}' -H 'Content-Type: application/json' -d "$2" "$1"
}
dc() { # dc <project> <envfile> <compose args...>
  docker compose --project-name "$1" --env-file "$2" -f "$ROOT/deploy/docker-compose.yml" "${@:3}"
}
register() { # 记录 project:envfile 供 EXIT 清理
  PROJECTS="$PROJECTS $1:$2"
}
health_poll() { # $1=base url（最多 ~90 秒）
  local base="$1" i code=""
  for i in $(seq 1 18); do
    code=$(http_get "$base/api/health" "$TMP/hp.json" 2>/dev/null) || code=000
    [ "$code" = 200 ] && break
    sleep 5
  done
  printf '%s' "$code"
}
db_writable() {
  if command -v jq >/dev/null 2>&1; then
    jq -r '.checks.db_writable // empty' "$TMP/hp.json" 2>/dev/null
  else
    grep -o '"db_writable":[a-z]*' "$TMP/hp.json" | head -1 | cut -d: -f2
  fi
}

# postgres 镜像只在此处预拉：base 镜像元数据查询失败（registry EOF/超时）
# 会打断整个 build，前置重试比半途失败好读。
for i in 1 2 3; do
  docker image inspect postgres:15-alpine >/dev/null 2>&1 && break
  log "pull postgres:15-alpine（尝试 $i）"
  docker pull postgres:15-alpine > "$LOGS/pg-pull.log" 2>&1 && break
  [ "$i" = 3 ] && { cat "$LOGS/pg-pull.log" >&2; bad "postgres 镜像拉取失败"; }
  sleep 5
done

# docker build：registry 抖动重试。真编译失败（Dockerfile/源码错）不会因重试变绿，
# 三次全败即按 build 失败处理。
docker_build() { # $1=project $2=envfile
  local i
  for i in 1 2 3; do
    dc "$1" "$2" build --progress=plain > "$LOGS/$1-build.log" 2>&1 && return 0
    [ "$i" = 3 ] && return 1
    log "build 重试 $i（registry 抖动）"
    sleep 5
  done
}

# ---------- 腿 A：sqlite 默认存储，dash/panel 经 Caddy 全链路 ----------
A="ferry-e2e-a$RANDOM"
AENV="$TMP/a.env"
cat > "$AENV" <<EOF
FERRY_ADMIN_USER=$ADMIN_USER
FERRY_ADMIN_PASSWORD=$ADMIN_PW
FERRY_HTTP_PORT=$A_PORT
FERRY_PANEL_PORT=$P_PORT
EOF
register "$A" "$AENV"

log "leg A 端口：dash=$A_PORT panel=$P_PORT project=$A"
log "leg A：build（sqlite）"
docker_build "$A" "$AENV" || { cat "$LOGS/$A-build.log" >&2; bad "leg A build 失败"; }

log "leg A：up -d"
dc "$A" "$AENV" up -d > "$LOGS/A-up.log" 2>&1 || {
  cat "$LOGS/A-up.log" >&2; dc "$A" "$AENV" ps >> "$LOGS/A-up.log" 2>&1 || true
  bad "leg A up -d 失败"; }

RUNNING=$(dc "$A" "$AENV" ps --format '{{.Service}} {{.State}}' 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')
expect "三服务全部运行" "ferry-dash ferry-panel ferry-server " "$RUNNING"

# compose 自带 healthcheck 真跑通（30s 间隔 + 15s start_period，等首轮结果）
HC="unknown"
for i in $(seq 1 12); do
  HC=$(dc "$A" "$AENV" ps --format '{{.Service}} {{.Health}}' 2>/dev/null \
    | awk '$1=="ferry-server"{print $2}')
  [ "$HC" = "healthy" ] && break
  sleep 5
done
expect "ferry-server 容器健康检查 healthy" "healthy" "$HC"

DASH="http://127.0.0.1:$A_PORT"
PANEL="http://127.0.0.1:$P_PORT"
CODE=$(health_poll "$DASH")
expect "dash Caddy 反代 /api/health 就绪（经前端而非容器直连）" "200" "$CODE"
expect "sqlite 落卷且可写" "true" "$(db_writable)"
# 首启库从未备份过 → degraded 属设计口径（compose healthcheck 注释同口径）
expect "首启从未备份=degraded（与 health 三态口径一致）" "degraded" "$(json_get status "$TMP/hp.json")"

# 静态产物托管 + history 回退
CODE=$(http_get "$DASH/" "$TMP/a-home.html")
expect "dash 首页 200" "200" "$CODE"
grep -qi '<html' "$TMP/a-home.html" && ok "dash 首页是 html 静态产物" || bad "dash 首页非 html"
CT=$(curl -sSI "$DASH/" | grep -i '^content-type:' | head -1 \
  | sed 's/^[Cc]ontent-[Tt]ype:[[:space:]]*//' | tr -d '\r' | cut -d';' -f1)
expect "dash 首页 content-type" "text/html" "$CT"
CODE=$(http_get "$DASH/nodes/e2e-404-refresh" "$TMP/a-deep.html")
expect "dash SPA 深链刷新返回 200" "200" "$CODE"
cmp -s "$TMP/a-home.html" "$TMP/a-deep.html" && ok "深链回退到 index.html（history 路由不 404）" \
  || bad "深链未回退 index.html"

# 后端顶层前缀反代：漏登记的故障形态是 GET 200 index.html / POST 405
CODE=$(http_post "$DASH/admin/login" '{"username":"admin","password":"wrong-pw"}' "$TMP/a-login-bad.json")
expect "POST /admin/login 经 Caddy 反代（未被静态兜底吞成 405）" "401" "$CODE"
CODE=$(http_post "$DASH/admin/login" \
  "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}" "$TMP/a-login.json")
expect "管理员首登（compose 容器内起库）" "200" "$CODE"
TOKEN=$(json_get token "$TMP/a-login.json")
[ -n "$TOKEN" ] && ok "登录签发 JWT" || bad "登录未返回 token"
CODE=$(curl -sS -o "$TMP/a-nodes.json" -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$DASH/api/nodes")
expect "Bearer 调 /api/nodes（dash 前端真实调用路径）" "200" "$CODE"
grep -q '^\[\]' "$TMP/a-nodes.json" && ok "节点列表为空数组（新库）" || bad "节点列表非空数组：$(cat "$TMP/a-nodes.json")"

CODE=$(http_get "$DASH/feed.xml" "$TMP/a-feed.xml")
expect "/feed.xml 反代 200" "200" "$CODE"
grep -q '<?xml' "$TMP/a-feed.xml" && ok "RSS 是 XML 而非 index.html" || bad "feed.xml 非 XML"

CODE=$(http_post "$DASH/distributor/login" '{"password":"wrong"}' "$TMP/a-dist.json")
expect "/distributor/login 顶层前缀反代（未落静态兜底）" "401" "$CODE"

# panel 第二实例同一 Caddyfile
CODE=$(http_get "$PANEL/" "$TMP/p-home.html")
expect "panel 首页 200" "200" "$CODE"
grep -qi '<html' "$TMP/p-home.html" && ok "panel 首页是 html 静态产物" || bad "panel 首页非 html"
CODE=$(http_get "$PANEL/subscriptions/e2e-404" "$TMP/p-deep.html")
expect "panel SPA 深链刷新 200" "200" "$CODE"
cmp -s "$TMP/p-home.html" "$TMP/p-deep.html" && ok "panel 深链回退 index.html" \
  || bad "panel 深链未回退 index.html"
CODE=$(health_poll "$PANEL")
expect "panel 侧 /api/health 反代 200" "200" "$CODE"
expect "panel 侧反代拿到后端真数据" "true" "$(db_writable)"

# 备份导出经 /admin 反代拿附件，健康由 never-backed-up 转 ok
BKOUT="$TMP/a-backup.bin"
BKHDR="$TMP/a-backup.hdr"
CODE=$(curl -sS -D "$BKHDR" -o "$BKOUT" -w '%{http_code}' \
  -H "Authorization: Bearer $TOKEN" "$DASH/admin/backup/db")
expect "GET /admin/backup/db 经反代导出" "200" "$CODE"
[ -s "$BKOUT" ] && ok "备份附件非空（$(stat -c%s "$BKOUT") 字节）" || bad "备份附件为空"
grep -qi '<html' "$BKOUT" && bad "备份附件是 index.html（反代把 /admin 落到了静态兜底）" \
  || ok "备份附件非静态页面"
BKN=$(grep -i '^content-disposition:' "$BKHDR" | head -1 | tr -d '\r' \
  | sed 's/.*filename="\(.*\)"/\1/')
[ -n "$BKN" ] && ok "备份文件名 $BKN" || bad "无 Content-Disposition 文件名"
# 容器内未配 FERRY_SECRET_KEY 时 keyinit 自动生成并持久化主密钥，
# 故默认部署的导出件即加密件（.enc）。
[ "${BKN##*.}" = enc ] && ok "默认部署备份即加密件（keyinit 自动主密钥生效）" \
  || bad "备份未加密（$BKN）"
CODE=$(http_get "$DASH/api/health" "$TMP/hp.json")
expect "手动备份后健康转 ok" "200" "$CODE"
expect "备份状态记账生效" "ok" "$(json_get status "$TMP/hp.json")"
FRESH=$(grep -o '"fresh":[a-z]*' "$TMP/hp.json" | tail -1 | cut -d: -f2)
expect "备份新鲜度记账（health 读回 backup.fresh）" "true" "$FRESH"

# teardown：端口释放、卷清干净
dc "$A" "$AENV" down -v --remove-orphans > "$LOGS/A-down.log" 2>&1 || bad "leg A down 失败"
CODE=200
for i in $(seq 1 20); do
  CODE=$(http_get "$DASH/api/health" "$TMP/hp.json" 2>/dev/null) || CODE=000
  [ "$CODE" = 000 ] && break
  sleep 0.5
done
[ "$CODE" = 000 ] && ok "down 后端口不再响应" || bad "down 后端口仍响应（$CODE）"
VOL=$(docker volume ls -q --filter "name=^${A}_ferry-data$" | wc -l)
expect "leg A 数据卷已删除" "0" "$VOL"

# ---------- 腿 B：postgres profile 存储切换（compose 记载路径，首次自动化） ----------
B="ferry-e2e-b$RANDOM"
BENV="$TMP/b.env"
cat > "$BENV" <<EOF
FERRY_ADMIN_USER=$ADMIN_USER
FERRY_ADMIN_PASSWORD=$ADMIN_PW
FERRY_HTTP_PORT=$A_PORT
FERRY_PANEL_PORT=$P_PORT
FERRY_DB_DRIVER=postgres
FERRY_DB_DSN=postgres://ferry:$PG_PW@ferry-postgres:5432/ferry?sslmode=disable
FERRY_PG_PASSWORD=$PG_PW
FERRY_KEYFILE=/data/ferry-keys.env
EOF
register "$B" "$BENV"

log "leg B 端口：dash=$A_PORT panel=$P_PORT project=$B"
log "leg B：build + up -d --profile postgres"
docker_build "$B" "$BENV" || { cat "$LOGS/$B-build.log" >&2; bad "leg B build 失败"; }
dc "$B" "$BENV" --profile postgres up -d > "$LOGS/B-up.log" 2>&1 || {
  cat "$LOGS/B-up.log" >&2; dc "$B" "$BENV" ps >> "$LOGS/B-up.log" 2>&1 || true
  bad "leg B up 失败"; }

RUNNING=$(dc "$B" "$BENV" ps --format '{{.Service}} {{.State}}' 2>/dev/null | awk '{print $1}' | sort -u | tr '\n' ' ')
expect "postgres profile 四服务全部运行" "ferry-dash ferry-panel ferry-postgres ferry-server " "$RUNNING"

# postgres 先起、服务端再连上并完成迁移：单次探活，最多等 ~120 秒
PGOK="no"
for i in $(seq 1 24); do
  CODE=$(http_get "$DASH/api/health" "$TMP/hp.json" 2>/dev/null) || CODE=000
  if [ "$CODE" = 200 ] && [ "$(db_writable)" = true ]; then
    PGOK=true
    break
  fi
  sleep 5
done
dc "$B" "$BENV" logs ferry-server >> "$LOGS/B-server.log" 2>&1 || true
dc "$B" "$BENV" logs ferry-postgres >> "$LOGS/B-pg.log" 2>&1 || true
expect "postgres 存储可写（迁移在 postgres 上完成）" "true" "$PGOK"
CODE=$(http_post "$DASH/admin/login" \
  "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}" "$TMP/b-login.json")
expect "管理员首登（数据落在 postgres 卷）" "200" "$CODE"
[ -n "$(json_get token "$TMP/b-login.json")" ] && ok "postgres 侧签发 JWT" || bad "postgres 侧未返回 token"

# postgres 存储下密钥无从推断落点：不配 FERRY_KEYFILE 时每轮重启换新密钥，
# 旧 JWT 全废、已加密备份解不开。断言显式配了之后密钥落卷且重启后不变。
MODE=$(dc "$B" "$BENV" exec -T ferry-server sh -c 'stat -c%a /data/ferry-keys.env 2>/dev/null')
expect "密钥文件落数据卷且 0600" "600" "$MODE"
dc "$B" "$BENV" exec -T ferry-server cat /data/ferry-keys.env > "$TMP/keyfile-1" 2>/dev/null
[ -s "$TMP/keyfile-1" ] && ok "密钥文件非空" || bad "密钥文件为空"
dc "$B" "$BENV" restart ferry-server > /dev/null 2>&1 || bad "leg B restart 失败"
PGOK="no"
for i in $(seq 1 24); do
  CODE=$(http_get "$DASH/api/health" "$TMP/hp.json" 2>/dev/null) || CODE=000
  [ "$CODE" = 200 ] && { PGOK="$(db_writable)"; [ "$PGOK" = true ] && break; }
  sleep 5
done
expect "重启后仍连得上 postgres" "true" "$PGOK"
dc "$B" "$BENV" exec -T ferry-server cat /data/ferry-keys.env > "$TMP/keyfile-2" 2>/dev/null
cmp -s "$TMP/keyfile-1" "$TMP/keyfile-2" \
  && ok "重启后密钥文件未变（JWT 与加密备份可恢复）" \
  || bad "重启后密钥被换掉（未配 FERRY_KEYFILE 的症状）"

# down 必须带 --profile：不带时 postgres 不在服务列表内，容器与卷会整个留下。
dc "$B" "$BENV" --profile postgres down -v --remove-orphans > "$LOGS/B-down.log" 2>&1 \
  || bad "leg B down 失败"
VOL=$(docker volume ls -q --filter "name=^${B}_ferry-pg$" | wc -l)
expect "leg B postgres 卷已删除" "0" "$VOL"

log "完成（退出码 0）"
