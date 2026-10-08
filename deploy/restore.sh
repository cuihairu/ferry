#!/usr/bin/env bash
# ferry 一键恢复（面板可用性 §3）：备份档 → 解密 → 导入数据库 → 证书 → 启动 → 健康冒烟。
# 执行前面板须已停止（systemctl stop / compose down），脚本不代管服务生命周期；
# 六步对应设计 §3：①部署检查 ②解密 .enc ③导入数据库（SQLite 覆盖 / pg_restore）
# ④恢复证书目录 ⑤启动（AutoMigrate 随启动补结构，agent 出站自动重连，节点侧零操作）
# ⑥ /api/health 冒烟。可复跑：数据段幂等（现有库留底+同目录暂存+原子覆盖），
# 留底按时间戳另存不覆盖。
#
# 用法（新机/同机恢复）：
#   restore.sh --backup <档> --data-dir <目录> [选项]
#   restore.sh --backup <档> --pg-dsn <dsn> [选项]
#
# 选项：
#   --backup <path>        备份档（ferry-backup-*.db 或 .enc，必填）
#   --data-dir <dir>       SQLite 数据目录（ferry.db 所在，SQLite 模式必填）
#   --key <master>         主密钥（.enc 必填；缺省取环境变量 FERRY_SECRET_KEY）
#   --ferry-bin <path>     ferry-server 可执行路径（.enc 解密用；缺省 PATH 查找）
#   --pg-dsn <dsn>         PostgreSQL 恢复：走 pg_restore --clean（档须自备
#                          pg_dump -Fc 产物；本仓周期备份只产 SQLite 档）
#   --cert-src <dir>       证书源目录（与 --cert-dst 成对，缺省跳过④）
#   --cert-dst <dir>       证书目标目录
#   --start-cmd <cmd>      启动命令（缺省跳过⑤，交由部署方式拉起）
#   --health-url <url>     /api/health 地址（缺省跳过⑥）
#   -h, --help             本帮助
#
# 示例：
#   restore.sh --backup backups/ferry-backup-20261008-000000.db.enc \
#     --data-dir /data --ferry-bin /usr/local/bin/ferry-server \
#     --start-cmd "systemctl restart ferry" \
#     --health-url http://127.0.0.1:8080/api/health

set -euo pipefail

die() { echo "restore: $*" >&2; exit 1; }
step() { echo; echo "== $* =="; }

BACKUP="" DATA_DIR="" KEY="${FERRY_SECRET_KEY:-}" FERRY_BIN=""
PG_DSN="" CERT_SRC="" CERT_DST="" START_CMD="" HEALTH_URL=""

while [ $# -gt 0 ]; do
  case "$1" in
    --backup)     [ $# -ge 2 ] || die "--backup 缺值"; BACKUP="$2"; shift 2 ;;
    --data-dir)   [ $# -ge 2 ] || die "--data-dir 缺值"; DATA_DIR="$2"; shift 2 ;;
    --key)        [ $# -ge 2 ] || die "--key 缺值"; KEY="$2"; shift 2 ;;
    --ferry-bin)  [ $# -ge 2 ] || die "--ferry-bin 缺值"; FERRY_BIN="$2"; shift 2 ;;
    --pg-dsn)     [ $# -ge 2 ] || die "--pg-dsn 缺值"; PG_DSN="$2"; shift 2 ;;
    --cert-src)   [ $# -ge 2 ] || die "--cert-src 缺值"; CERT_SRC="$2"; shift 2 ;;
    --cert-dst)   [ $# -ge 2 ] || die "--cert-dst 缺值"; CERT_DST="$2"; shift 2 ;;
    --start-cmd)  [ $# -ge 2 ] || die "--start-cmd 缺值"; START_CMD="$2"; shift 2 ;;
    --health-url) [ $# -ge 2 ] || die "--health-url 缺值"; HEALTH_URL="$2"; shift 2 ;;
    -h|--help)    sed -n '2,30p' "$0"; exit 0 ;;
    *)            die "未知参数：$1（--help 看用法）" ;;
  esac
done

# ---- 入参校验：缺参/缺档明确报错非 0 ----
[ -n "$BACKUP" ] || die "缺 --backup（备份档路径）"
[ -s "$BACKUP" ] || die "备份档不存在或为空：$BACKUP"

if [ -n "$PG_DSN" ]; then
  MODE=pg
else
  MODE=sqlite
  [ -n "$DATA_DIR" ] || die "SQLite 恢复缺 --data-dir（ferry.db 所在目录）"
  [ -d "$DATA_DIR" ] || die "数据目录不存在：$DATA_DIR（--data-dir）"
  STAGE="$DATA_DIR/.ferry-restore-stage.db"
fi

# ---- ① 部署检查：ferry-server 可执行即视为已部署（版本同或更高由部署者保证）----
step "① 部署检查"
if [ -n "$FERRY_BIN" ]; then
  [ -x "$FERRY_BIN" ] || die "ferry 二进制不可执行：$FERRY_BIN（先部署 ferry，同版本或更高）"
  echo "ferry 二进制：$FERRY_BIN"
else
  echo "跳过（未提供 --ferry-bin；.enc 档会自动在 PATH 找 ferry-server）"
fi

# ---- ② 解密 .enc：openssl enc 无 AES-GCM 能力，借 ferry-server 自身还原 ----
SRC="$BACKUP"
case "$BACKUP" in
  *.enc)
    [ -n "$KEY" ] || die ".enc 档解密需要主密钥：--key 或环境变量 FERRY_SECRET_KEY"
    BIN="$FERRY_BIN"
    [ -n "$BIN" ] || BIN="$(command -v ferry-server || true)"
    [ -n "$BIN" ] && [ -x "$BIN" ] || die "找不到可执行的 ferry-server（--ferry-bin）；.enc 档须借 ferry 自身解密（openssl 不支持 AES-GCM）"
    if [ "$MODE" = pg ]; then
      STAGE="$(mktemp "${TMPDIR:-/tmp}/ferry-restore-XXXXXX.dump")"
    else
      STAGE="$DATA_DIR/.ferry-restore-stage.db"
    fi
    step "② 解密"
    "$BIN" backup-decrypt -key "$KEY" -in "$BACKUP" -out "$STAGE" \
      || die "解密失败：主密钥不符或备份档损坏"
    SRC="$STAGE"
    echo "$BACKUP -> $STAGE"
    ;;
  *)
    step "② 解密"
    echo "明文档，跳过"
    ;;
esac

# ---- ③ 导入数据库 ----
step "③ 导入数据库"
if [ "$MODE" = pg ]; then
  command -v pg_restore >/dev/null 2>&1 || die "pg_restore 不在 PATH（PG 恢复需 pg 工具链）"
  magic="$(head -c 5 "$SRC" 2>/dev/null || true)"
  [ "$magic" = "PGDMP" ] || die "档不是 pg_restore 可读的自备 dump（须 pg_dump -Fc 产物）：$SRC"
  pg_restore --clean --if-exists --dbname="$PG_DSN" "$SRC" \
    || die "pg_restore 导入失败（确认面板已停、DSN 正确）"
  echo "pg_restore 导入完成：$SRC"
else
  DB="$DATA_DIR/ferry.db"
  if [ -f "$DB" ]; then
    bak="$DB.pre-restore-$(date +%Y%m%d-%H%M%S)"
    cp -p "$DB" "$bak" || die "留底现有库失败：$DB"
    if [ -f "$DB-wal" ]; then cp -p "$DB-wal" "$bak-wal"; fi
    if [ -f "$DB-shm" ]; then cp -p "$DB-shm" "$bak-shm"; fi
    echo "现有库留底：$bak"
  fi
  if [ "$SRC" != "$STAGE" ]; then
    cp "$SRC" "$STAGE" || die "暂存备份档失败：$STAGE"
  fi
  # 先清旧库 WAL/SHM 残留再原子换主库，防新主库打开时回放旧 WAL。
  rm -f "$DB-wal" "$DB-shm"
  mv "$STAGE" "$DB" || die "覆盖 $DB 失败"
  echo "已导入：$SRC -> $DB（原子覆盖）"
fi
# 解密用过的临时档清理（SQLite 模式的暂存已被 mv 走，rm -f 兜底无害）。
if [ "$SRC" != "$BACKUP" ] && [ "$MODE" = pg ]; then rm -f "$SRC"; fi

# ---- ④ 恢复证书目录（mTLS CA / acme.sh 工作目录等，缺省跳过）----
step "④ 证书目录"
if [ -n "$CERT_SRC" ] && [ -n "$CERT_DST" ]; then
  [ -d "$CERT_SRC" ] || die "证书源目录不存在：$CERT_SRC"
  mkdir -p "$CERT_DST"
  cp -a "$CERT_SRC/." "$CERT_DST/"
  echo "$CERT_SRC -> $CERT_DST"
elif [ -n "$CERT_SRC" ] || [ -n "$CERT_DST" ]; then
  die "--cert-src 与 --cert-dst 须成对提供"
else
  echo "跳过（未提供 --cert-src/--cert-dst）"
fi

# ---- ⑤ 启动（AutoMigrate 随进程启动补齐结构；agent 出站自动重连）----
step "⑤ 启动"
if [ -n "$START_CMD" ]; then
  echo "执行：$START_CMD"
  bash -c "$START_CMD" || die "启动命令失败（面板若已在运行属冲突：先停再跑，或重跑时省 --start-cmd）"
else
  echo "跳过（未提供 --start-cmd，交由部署方式拉起）"
fi

# ---- ⑥ /api/health 冒烟（ok/degraded 均 200 可读即算通，异常如实报在 body）----
step "⑥ 健康冒烟"
if [ -n "$HEALTH_URL" ]; then
  command -v curl >/dev/null 2>&1 || die "冒烟需要 curl（--health-url）"
  body=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    body="$(curl -fsS --max-time 5 "$HEALTH_URL" 2>/dev/null || true)"
    case "$body" in
      *'"status"'*) break ;;
      *) body=""; sleep 1 ;;
    esac
  done
  [ -n "$body" ] || die "健康检查无响应：$HEALTH_URL（面板未起或地址不对）"
  echo "$HEALTH_URL"
  echo "$body"
else
  echo "跳过（未提供 --health-url）"
fi

echo
echo "恢复完成。dash 冒烟建议：节点在线数 / 订阅可达 / 订单可读（面板可用性 §3⑥）。"
