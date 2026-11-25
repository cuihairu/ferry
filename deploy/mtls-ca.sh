#!/usr/bin/env bash
# ferry panel-side mTLS certificate tool (A-24): a self-signed CA lives on the panel host and issues
# node client certificates.
# Rationale per `docs/design/安全设计.md` §2.2: the CA private key stays on the panel host only; with mTLS enabled,
# token+certificate form two factors; whether the panel side enforces client certificate verification is
# decided by the reverse proxy / panel TLS configuration — this script only generates and renews the
# certificate material.
#
# Usage (run on the panel host):
#   mtls-ca.sh init [dir]                       create the CA (default deploy/mtls; refused if it already exists)
#   mtls-ca.sh issue <node-name> [days] [dir]  issue <dir>/<node-name>.{crt,key} (default 825 days)
set -euo pipefail

die() { echo "mtls-ca: $*" >&2; exit 1; }
CMD="${1:-}"; shift || true

case "$CMD" in
  init)
    DIR="${1:-$(dirname "$0")/mtls}"
    if [ -e "$DIR/ca.crt" ] || [ -e "$DIR/ca.key" ]; then
      die "CA 已存在：$DIR/ca.{crt,key}（换 CA 等于全节点重签，确认后手动删除）"
    fi
    mkdir -p "$DIR"
    openssl ecparam -name prime256v1 -genkey -noout -out "$DIR/ca.key"
    openssl req -new -x509 -key "$DIR/ca.key" -days 3650 \
      -subj "/CN=ferry-mtls-ca" -out "$DIR/ca.crt"
    chmod 600 "$DIR/ca.key"
    echo "CA 已生成：$DIR/ca.crt（分发到 agent --ca）+ $DIR/ca.key（只留面板机签发用）"
    ;;
  issue)
    NAME="${1:-}"; DAYS="${2:-825}"; DIR="${3:-$(dirname "$0")/mtls}"
    [ -n "$NAME" ] || die "用法：mtls-ca.sh issue <节点名> [天数] [目录]"
    [[ "$DAYS" =~ ^[0-9]+$ ]] || die "天数须为数字：$DAYS"
    [ -f "$DIR/ca.crt" ] && [ -f "$DIR/ca.key" ] || die "先 init：mtls-ca.sh init $DIR"
    openssl ecparam -name prime256v1 -genkey -noout -out "$DIR/$NAME.key"
    openssl req -new -key "$DIR/$NAME.key" -subj "/CN=$NAME" -out "$DIR/$NAME.csr"
    cat > "$DIR/$NAME.ext" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=clientAuth
EOF
    openssl x509 -req -in "$DIR/$NAME.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
      -CAcreateserial -days "$DAYS" -extfile "$DIR/$NAME.ext" -out "$DIR/$NAME.crt" 2>/dev/null
    rm -f "$DIR/$NAME.csr" "$DIR/$NAME.ext"
    chmod 600 "$DIR/$NAME.key"
    echo "已签发 $NAME（$DAYS 天）："
    echo "  agent-install.sh --ca $DIR/ca.crt --cert $DIR/$NAME.crt --key $DIR/$NAME.key"
    ;;
  *)
    sed -n '2,10p' "$0"; exit 1 ;;
esac
