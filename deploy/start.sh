#!/usr/bin/env bash
# ferry one-click startup: generate .env -> build and start services -> wait for readiness -> verify DB creation.
#
#   ./deploy/start.sh                       # sqlite by default
#   ./deploy/start.sh --profile postgres     # also bring up postgres storage
#
# Stop services: docker compose --env-file .env -f deploy/docker-compose.yml down
#   (postgres deploys must pass --profile postgres, otherwise the postgres container and volume are left behind)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMPOSE=(docker compose --env-file .env -f deploy/docker-compose.yml)

# --profile is a compose global flag and must precede the subcommand; passing
# it through to `up` as-is raises an unknown flag error. So --profile is
# pulled out and inserted into the front of the command, while the remaining
# arguments (e.g. --scale) keep being forwarded to up.
PROFILE_ARGS=()
UP_ARGS=()
i=1
while [ "$i" -le "$#" ]; do
  a="${!i}"
  case "$a" in
    --profile=*) PROFILE_ARGS+=(--profile "${a#--profile=}") ;;
    --profile)
      i=$((i + 1))
      PROFILE_ARGS+=(--profile "${!i}")
      ;;
    *) UP_ARGS+=("$a") ;;
  esac
  i=$((i + 1))
done
COMPOSE=(docker compose ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"} --env-file .env -f deploy/docker-compose.yml)

# postgres profile preflight: with FERRY_PG_PASSWORD unset the container refuses
# to start, so failing early is easier to read than waiting out the 180s timeout.
# The password in the DSN must match it (user=ferry is created by compose).
PG_PROFILE=0
for p in ${PROFILE_ARGS[@]+"${PROFILE_ARGS[@]}"}; do
  [ "$p" = postgres ] && PG_PROFILE=1
done
if [ "$PG_PROFILE" = 1 ] \
  && [ -z "$(grep -E '^FERRY_PG_PASSWORD=.+' .env 2>/dev/null | head -1)" ]; then
  echo "!! --profile postgres 需要 .env 设置 FERRY_PG_PASSWORD（postgres 容器会拒启）"
  echo "!! 并同步 FERRY_DB_DRIVER=postgres、"
  echo "!! FERRY_DB_DSN=postgres://ferry:<同一密码>@ferry-postgres:5432/ferry?sslmode=disable"
  exit 1
fi

echo "==> ferry one-click startup (repo: $ROOT)"

# First run: if .env is missing, generate it from .env.example; if it exists, use it as-is and never overwrite it.
if [ ! -f .env ]; then
  cp .env.example .env
  echo "==> .env generated from .env.example — edit it to set ports / secrets"
fi

echo "==> building and starting services"
set +e
"${COMPOSE[@]}" up -d --build ${UP_ARGS[@]+"${UP_ARGS[@]}"}
UP_RC=$?
set -e
if [ "$UP_RC" -ne 0 ]; then
  echo ""
  echo "!! services did not start (compose exit $UP_RC)"
  echo "!! if it is a bind error, change FERRY_HTTP_PORT / FERRY_PANEL_PORT in .env"
  echo "!! if it is a build error, the failing step is printed above"
  echo "!! ${COMPOSE[*]} logs --tail 100"
  "${COMPOSE[@]}" logs --tail 100 2>&1 || true
  exit "$UP_RC"
fi

echo "==> waiting for ferry-server /api/health (up to 180s)"
deadline=$((SECONDS + 180))
until "${COMPOSE[@]}" exec -T ferry-server \
    wget -q -O /dev/null http://127.0.0.1:8080/api/health >/dev/null 2>&1; do
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo ""
    echo "!! ferry-server did not become healthy within 180s"
    echo "!! last log lines:"
    "${COMPOSE[@]}" logs --tail 100 ferry-server || true
    echo "!! create / restore path: deploy/MIGRATION-RUNBOOK.md"
    exit 1
  fi
  sleep 3
done

# DB verification: table creation/migrations run automatically via storage.Open's AutoMigrate
# (idempotent and repeatable); here we only confirm the DB file actually landed on disk —
# if it has not, startup was interrupted for some other reason.
# Inside the container FERRY_DB_DRIVER is the authoritative value after compose interpolation,
# so we do not re-parse .env ourselves.
DB_DRIVER="$("${COMPOSE[@]}" exec -T ferry-server \
  sh -c 'echo "${FERRY_DB_DRIVER:-sqlite}"' 2>/dev/null || true)"
DB_DRIVER="${DB_DRIVER:-sqlite}"
if [ "$DB_DRIVER" = "sqlite" ]; then
  if "${COMPOSE[@]}" exec -T ferry-server test -s /data/ferry.db; then
    echo "==> database created: /data/ferry.db (volume ferry-data)"
  else
    echo "!! /data/ferry.db is missing after startup"
    echo "!! create / restore path: deploy/MIGRATION-RUNBOOK.md"
    exit 1
  fi
fi

# Effective ports follow the actual compose mapping (read from the mapping when the user changed .env).
host_port() {
  "${COMPOSE[@]}" port "$1" 80 2>/dev/null | tail -n1 | sed 's/.*://' | tr -d '[:space:]'
}
HTTP_PORT="$(host_port ferry-dash)"
PANEL_PORT="$(host_port ferry-panel)"
HTTP_PORT="${HTTP_PORT:-8080}"
PANEL_PORT="${PANEL_PORT:-8081}"

echo ""
echo "== ferry is up =="
echo "  admin dashboard : http://127.0.0.1:${HTTP_PORT}/"
echo "  user panel      : http://127.0.0.1:${PANEL_PORT}/"
echo "  api health      : http://127.0.0.1:${HTTP_PORT}/api/health"
echo ""
echo "  first dashboard login runs the admin bootstrap"
echo "  stop            : ${COMPOSE[*]} down"
echo "  logs            : ${COMPOSE[*]} logs -f"
