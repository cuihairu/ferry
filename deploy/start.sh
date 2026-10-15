#!/usr/bin/env bash
# ferry 一键启动：生成 .env → 构建并起服务 → 等就绪 → 校验建库。
#
#   ./deploy/start.sh                       # 默认 sqlite
#   ./deploy/start.sh --profile postgres     # 同时起 postgres 存储
#
# 停服务：docker compose --env-file .env -f deploy/docker-compose.yml down
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMPOSE=(docker compose --env-file .env -f deploy/docker-compose.yml)

echo "==> ferry one-click startup (repo: $ROOT)"

# 首次启动：.env 不存在则从 .env.example 生成；已存在则原样使用，绝不覆盖。
if [ ! -f .env ]; then
  cp .env.example .env
  echo "==> .env generated from .env.example — edit it to set ports / secrets"
fi

echo "==> building and starting services"
set +e
"${COMPOSE[@]}" up -d --build "$@"
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

# 建库校验：建表/迁移随 storage.Open 的 AutoMigrate 自动跑（幂等，可重复
# 执行），这里只确认库文件真的落盘——没落盘说明启动被别的原因打断。
# 容器内 FERRY_DB_DRIVER 是 compose 插值后的权威值，不再自行解析 .env。
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

# 生效端口以 compose 实际映射为准（用户在 .env 里改过就从映射里读）。
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
