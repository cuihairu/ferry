#!/usr/bin/env bash
# compose real-deploy smoke (closing FERRY-004): a throwaway compose project + temp volume runs a
# real docker compose build/up, and the Caddy front end verifies three things — static asset hosting,
# history routing fallback, and backend top-level prefix reverse proxy (the typical failure shape
# when a route is missing from the registry: GET returns index.html(200) and POST returns 405,
# which this script's POST assertions catch); two legs: sqlite default storage + postgres profile
# storage switch (a path documented in compose comments but never exercised by any automation).
# Ends with down -v to clear containers and volumes; the random project name cannot clash with a
# real deployment and never touches the host's panel data.
# Nightly only: build is a real compile (both Go front ends + backend), unsuitable for the push gate.
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
free_port() { # find an unoccupied TCP port in [base, base+400] (the panel often already holds 18080 on this host)
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
PROJECTS="" # space-separated "project:envfile", each downed with -v on EXIT
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
expect() { # $1=description $2=expected $3=actual
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
http_get() { # $1=url $2=outfile (returns the status code)
  curl -sS -o "$2" -w '%{http_code}' "$1"
}
http_post() { # $1=url $2=body $3=outfile (returns the status code)
  curl -sS -o "$3" -w '%{http_code}' -H 'Content-Type: application/json' -d "$2" "$1"
}
dc() { # dc <project> <envfile> <compose args...>
  docker compose --project-name "$1" --env-file "$2" -f "$ROOT/deploy/docker-compose.yml" "${@:3}"
}
register() { # record project:envfile for EXIT cleanup
  PROJECTS="$PROJECTS $1:$2"
}
health_poll() { # $1=base url $2=bearer token (up to ~90 seconds)
  local base="$1" token="$2" i code=""
  for i in $(seq 1 18); do
    if [ -n "$token" ]; then
      code=$(curl -sS -o "$TMP/hp.json" -w '%{http_code}' -H "Authorization: Bearer $token" "$base/api/health" 2>/dev/null) || code=000
    else
      code=$(http_get "$base/api/health" "$TMP/hp.json" 2>/dev/null) || code=000
    fi
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

# The postgres image is pre-pulled only here: a failed base image metadata lookup
# (registry EOF/timeout) would abort the whole build, and an early retry reads better
# than a mid-flight failure.
for i in 1 2 3; do
  docker image inspect postgres:15-alpine >/dev/null 2>&1 && break
  log "pull postgres:15-alpine（尝试 $i）"
  docker pull postgres:15-alpine > "$LOGS/pg-pull.log" 2>&1 && break
  [ "$i" = 3 ] && { cat "$LOGS/pg-pull.log" >&2; bad "postgres 镜像拉取失败"; }
  sleep 5
done

# docker build: retry on registry flakiness. A genuine compile failure (Dockerfile/source
# error) does not turn green on retry; three failures in a row is treated as a build failure.
docker_build() { # $1=project $2=envfile
  local i
  for i in 1 2 3; do
    dc "$1" "$2" build --progress=plain > "$LOGS/$1-build.log" 2>&1 && return 0
    [ "$i" = 3 ] && return 1
    log "build 重试 $i（registry 抖动）"
    sleep 5
  done
}

# ---------- Leg A: sqlite default storage, dash/panel full path through Caddy ----------
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

# The compose healthcheck actually runs (30s interval + 15s start_period, wait for the first result)
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
# After the P2 health auth change (router.go healthGuard), the loopback exemption on /api/health
# only trusts the direct ClientIP; a reverse-proxied path (host curl -> Caddy -> server) does not
# land on loopback — so health probes must carry credentials.
# Readiness criterion: a login with bad credentials first returns 401 (the backend behind Caddy has
# already answered), then a real login fetches the JWT.
for i in $(seq 1 18); do
  CODE=$(http_post "$DASH/admin/login" '{"username":"admin","password":"wrong-pw"}' "$TMP/a-warm.json" 2>/dev/null) || CODE=000
  [ "$CODE" = "401" ] && break
  sleep 5
done
expect "dash Caddy 反代后端就绪（/admin/login 拒坏凭据 401，未被静态兜底吞成 405）" "401" "$CODE"
CODE=$(http_post "$DASH/admin/login" \
  "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}" "$TMP/a-login.json")
expect "管理员首登（compose 容器内起库）" "200" "$CODE"
TOKEN=$(json_get token "$TMP/a-login.json")
[ -n "$TOKEN" ] && ok "登录签发 JWT" || bad "登录未返回 token"
CODE=$(health_poll "$DASH" "$TOKEN")
expect "dash Caddy 反代 /api/health 就绪（凭据探活，经前端而非容器直连）" "200" "$CODE"
expect "sqlite 落卷且可写" "true" "$(db_writable)"
# The first-run DB has never been backed up -> degraded is by design (same criterion as the compose healthcheck comment)
expect "首启从未备份=degraded（与 health 三态口径一致）" "degraded" "$(json_get status "$TMP/hp.json")"

# Static asset hosting + history fallback
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

# Backend top-level prefix reverse proxy is already covered by the readiness probe section (a 401 on bad credentials proves the real backend answered)
CODE=$(curl -sS -o "$TMP/a-nodes.json" -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$DASH/api/nodes")
expect "Bearer 调 /api/nodes（dash 前端真实调用路径）" "200" "$CODE"
grep -q '^\[\]' "$TMP/a-nodes.json" && ok "节点列表为空数组（新库）" || bad "节点列表非空数组：$(cat "$TMP/a-nodes.json")"

CODE=$(http_get "$DASH/feed.xml" "$TMP/a-feed.xml")
expect "/feed.xml 反代 200" "200" "$CODE"
grep -q '<?xml' "$TMP/a-feed.xml" && ok "RSS 是 XML 而非 index.html" || bad "feed.xml 非 XML"

CODE=$(http_post "$DASH/distributor/login" '{"password":"wrong"}' "$TMP/a-dist.json")
expect "/distributor/login 顶层前缀反代（未落静态兜底）" "401" "$CODE"

# Second panel instance off the same Caddyfile
CODE=$(http_get "$PANEL/" "$TMP/p-home.html")
expect "panel 首页 200" "200" "$CODE"
grep -qi '<html' "$TMP/p-home.html" && ok "panel 首页是 html 静态产物" || bad "panel 首页非 html"
CODE=$(http_get "$PANEL/subscriptions/e2e-404" "$TMP/p-deep.html")
expect "panel SPA 深链刷新 200" "200" "$CODE"
cmp -s "$TMP/p-home.html" "$TMP/p-deep.html" && ok "panel 深链回退 index.html" \
  || bad "panel 深链未回退 index.html"
CODE=$(health_poll "$PANEL" "$TOKEN")
expect "panel 侧 /api/health 反代 200" "200" "$CODE"
expect "panel 侧反代拿到后端真数据" "true" "$(db_writable)"

# Fetch the backup export attachment through the /admin reverse proxy; health moves from never-backed-up to ok
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
# When FERRY_SECRET_KEY is not configured in the container, keyinit generates and persists the master
# key automatically, so the default deployment's export is already an encrypted (.enc) artifact.
[ "${BKN##*.}" = enc ] && ok "默认部署备份即加密件（keyinit 自动主密钥生效）" \
  || bad "备份未加密（$BKN）"
CODE=$(curl -sS -o "$TMP/hp.json" -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$DASH/api/health")
expect "手动备份后健康转 ok" "200" "$CODE"
expect "备份状态记账生效" "ok" "$(json_get status "$TMP/hp.json")"
FRESH=$(grep -o '"fresh":[a-z]*' "$TMP/hp.json" | tail -1 | cut -d: -f2)
expect "备份新鲜度记账（health 读回 backup.fresh）" "true" "$FRESH"

# teardown: ports released, volumes wiped
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

# ---------- Leg B: postgres profile storage switch (the compose-documented path, first time automated) ----------
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

# postgres starts first, then the server connects and finishes migrations: a single probe, at most ~120 seconds.
# With healthGuard, /api/health must be probed with credentials: first wait for /admin/login to reject bad
# credentials with 401 (backend ready), then log in for the JWT and poll health.
BTOKEN=""
for i in $(seq 1 24); do
  CODE=$(http_post "$DASH/admin/login" '{"username":"admin","password":"wrong-pw"}' "$TMP/b-warm.json" 2>/dev/null) || CODE=000
  [ "$CODE" = "401" ] && break
  sleep 5
done
if [ "$CODE" = "401" ]; then
  http_post "$DASH/admin/login" \
    "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PW\"}" "$TMP/b-login.json" >/dev/null 2>&1
  BTOKEN=$(json_get token "$TMP/b-login.json")
fi
PGOK="no"
for i in $(seq 1 24); do
  if [ -n "$BTOKEN" ]; then
    CODE=$(curl -sS -o "$TMP/hp.json" -w '%{http_code}' -H "Authorization: Bearer $BTOKEN" "$DASH/api/health" 2>/dev/null) || CODE=000
  else
    CODE=$(http_get "$DASH/api/health" "$TMP/hp.json" 2>/dev/null) || CODE=000
  fi
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

# With postgres storage there is no way to infer where the key lands: without FERRY_KEYFILE a fresh key
# is generated on every restart, invalidating all old JWTs and making encrypted backups unreadable.
# Assert that with an explicit keyfile the key lands on the volume and survives a restart.
MODE=$(dc "$B" "$BENV" exec -T ferry-server sh -c 'stat -c%a /data/ferry-keys.env 2>/dev/null')
expect "密钥文件落数据卷且 0600" "600" "$MODE"
dc "$B" "$BENV" exec -T ferry-server cat /data/ferry-keys.env > "$TMP/keyfile-1" 2>/dev/null
[ -s "$TMP/keyfile-1" ] && ok "密钥文件非空" || bad "密钥文件为空"
dc "$B" "$BENV" restart ferry-server > /dev/null 2>&1 || bad "leg B restart 失败"
PGOK="no"
for i in $(seq 1 24); do
  if [ -n "$BTOKEN" ]; then
    CODE=$(curl -sS -o "$TMP/hp.json" -w '%{http_code}' -H "Authorization: Bearer $BTOKEN" "$DASH/api/health" 2>/dev/null) || CODE=000
  else
    CODE=$(http_get "$DASH/api/health" "$TMP/hp.json" 2>/dev/null) || CODE=000
  fi
  [ "$CODE" = 200 ] && { PGOK="$(db_writable)"; [ "$PGOK" = true ] && break; }
  sleep 5
done
expect "重启后仍连得上 postgres" "true" "$PGOK"
dc "$B" "$BENV" exec -T ferry-server cat /data/ferry-keys.env > "$TMP/keyfile-2" 2>/dev/null
cmp -s "$TMP/keyfile-1" "$TMP/keyfile-2" \
  && ok "重启后密钥文件未变（JWT 与加密备份可恢复）" \
  || bad "重启后密钥被换掉（未配 FERRY_KEYFILE 的症状）"

# down must include --profile: otherwise postgres is not in the service list and its container and volume are left behind entirely.
dc "$B" "$BENV" --profile postgres down -v --remove-orphans > "$LOGS/B-down.log" 2>&1 \
  || bad "leg B down 失败"
VOL=$(docker volume ls -q --filter "name=^${B}_ferry-pg$" | wc -l)
expect "leg B postgres 卷已删除" "0" "$VOL"

log "完成（退出码 0）"
