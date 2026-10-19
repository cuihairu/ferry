#!/usr/bin/env bash
# agent-install.sh 原子替换/回滚/失败诊断的沙箱回归（FERRY-005）：不碰真
# systemd 也不需要 root——PATH 前置假件（id/systemctl/journalctl/sleep），
# 二进制走 --binary、安装/配置/unit 目录全落临时沙箱，跑的是真脚本本体。
# 链路：首装成功 / 升级成功 / 升级失败回滚 / 首装失败清理 / 既有配置保留 /
# QUIC sidecar 成功与升级失败回滚 / 下载链路（FERRY-002：latest 解析、
# agent 与 quic 伴随 .sha256 校验、尚无稳定版早失败）。
# CI 接线见 .github/workflows/test.yml deploy-scripts job；本地直接跑本文件。
set -u
cd "$(dirname "$0")/.."

pass=0 fail=0
ok() { pass=$((pass+1)); echo "ok - $1"; }
bad() { fail=$((fail+1)); echo "FAIL - $1" >&2; }
assert_eq() { # $1=描述 $2=期望 $3=实际
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1：want [$2] got [$3]"; fi
}
assert_contains() { # $1=描述 $2=应含子串 $3=全文
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1：缺 [$2]" >&2 ;; esac
}
assert_absent() { # $1=描述 $2=不应存在的路径
  if [ ! -e "$2" ]; then ok "$1"; else bad "$1：[$2] 不应存在"; fi
}

SB=$(mktemp -d)
trap 'rm -rf "$SB"' EXIT
mkdir -p "$SB/bin" "$SB/etc" "$SB/systemd" "$SB/fakebin"

# ---- 假件：id 报 root、systemctl 记录调用并按 $FAKE_FAIL/<服务名> 模拟起失败
# （逐服务可控，能单测 sidecar 失败而 agent 正常的组合）、journalctl 给固定
# 诊断行、sleep 空转（wait 轮询不拖时长）、curl 对 /api/enroll 报 201----
FAKE_LOG="$SB/fakebin/calls.log"; : > "$FAKE_LOG"
FAKE_FAIL="$SB/fakebin/FAIL"; mkdir -p "$FAKE_FAIL"
cat > "$SB/fakebin/id" <<'EOF'
#!/bin/sh
echo 0
EOF
cat > "$SB/fakebin/systemctl" <<EOF
#!/bin/sh
echo "systemctl \$*" >> "$FAKE_LOG"
case "\$1 \$2" in
  "is-active --quiet") [ -f "$FAKE_FAIL/\$3" ] && exit 3 || exit 0 ;;
  *) exit 0 ;;
esac
EOF
cat > "$SB/fakebin/journalctl" <<EOF
#!/bin/sh
echo "journalctl \$*" >> "$FAKE_LOG"
echo "fake-diagnostic: unit failed to start"
EOF
cat > "$SB/fakebin/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
# 假 curl：/api/enroll 把注册响应写进 -o 文件并报 201；GitHub API 出 v0.9.1
# （latest 解析链路，置 latest-404 标记则报 404 模拟尚无稳定版）；tar.gz 与
# .sha256 供预置发布件（.sha256 按 Release 惯例记原始长文件名，暴露对照名错位）
cat > "$SB/fakebin/curl" <<EOF
#!/bin/sh
echo "curl \$*" >> "$FAKE_LOG"
out=""; prev=""
for a in "\$@"; do
  [ "\$prev" = "-o" ] && out="\$a"
  prev="\$a"
done
case "\$*" in
  */api/enroll*)
    printf '{"token":"enroll-token-1","id":7}' > "\$out"
    echo 201
    ;;
  *releases/latest*)
    if [ -f "$FAKE_FAIL/latest-404" ]; then
      echo 404
    else
      printf '{"tag_name":"v0.9.1"}' > "\$out"
      echo 200
    fi
    ;;
  *ferry-agent_*.tar.gz.sha256*)
    cp "$SB/dl/agent.sha256" "\$out"
    ;;
  *ferry-agent_*linux_*.tar.gz*)
    cp "$SB/dl/ferry-agent_0.9.1_linux_amd64.tar.gz" "\$out"
    ;;
  *ferry-quic_*.tar.gz.sha256*)
    cp "$SB/dl/quic.sha256" "\$out"
    ;;
  *ferry-quic_*linux_*.tar.gz*)
    cp "$SB/dl/ferry-quic_0.9.1_linux_amd64.tar.gz" "\$out"
    ;;
  *) exit 22 ;;
esac
EOF
chmod +x "$SB/fakebin"/*

# fake-agent：可执行占位（systemd 是假件，不会真跑它）
printf '#!/bin/sh\necho ferry-agent-fake\n' > "$SB/fake-agent"
chmod +x "$SB/fake-agent"

# 预置发布件（下载链路供件）：包内二进制名 ferry-agent/ferry-quic；.sha256
# 按 GitHub Release 惯例记录原始长文件名，安装脚本须把对照名改写为本地
# 下载名（agent.tar.gz/quic.tar.gz）再校验
mkdir -p "$SB/dl"
cp "$SB/fake-agent" "$SB/dl/ferry-agent"
printf '#!/bin/sh\necho ferry-quic-fake\n' > "$SB/dl/ferry-quic"
tar -czf "$SB/dl/ferry-agent_0.9.1_linux_amd64.tar.gz" -C "$SB/dl" ferry-agent
tar -czf "$SB/dl/ferry-quic_0.9.1_linux_amd64.tar.gz" -C "$SB/dl" ferry-quic
( cd "$SB/dl" && sha256sum ferry-agent_0.9.1_linux_amd64.tar.gz > agent.sha256 \
  && sha256sum ferry-quic_0.9.1_linux_amd64.tar.gz > quic.sha256 )

run_install() { # 其余参数透传给安装脚本；结果在 OUT/RC
  OUT=$(PATH="$SB/fakebin:$PATH" FERRY_SYSTEMD_UNIT_DIR="$SB/systemd" \
    bash deploy/agent-install.sh --panel https://panel.example.com \
    --binary "$SB/fake-agent" --install "$SB/bin/ferry-agent" \
    --config "$SB/etc/agent.json" "$@" 2>&1)
  RC=$?
}

run_install_fetch() { # 不带 --binary：走下载链路（假 curl 供件）；其余同 run_install
  OUT=$(PATH="$SB/fakebin:$PATH" FERRY_SYSTEMD_UNIT_DIR="$SB/systemd" \
    bash deploy/agent-install.sh --panel https://panel.example.com \
    --install "$SB/bin/ferry-agent" --config "$SB/etc/agent.json" "$@" 2>&1)
  RC=$?
}

UNIT="$SB/systemd/ferry-agent.service"
NEW_MARK='echo ferry-agent-fake'

# ---- 1. 首装成功：二进制落位、配置/unit 齐全、无 .new/.prev 残留 ----
run_install --register hk1 --enroll-token dummy --address hk1.example.com
assert_eq "首装成功退出码" 0 "$RC"
assert_eq "首装二进制落位" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"
assert_eq "unit 已写" "1" "$(grep -c 'ExecStart=' "$UNIT")"
assert_contains "unit 带角色参数缺省启动行" "-config $SB/etc/agent.json" "$(cat "$UNIT")"
assert_contains "配置含注册响应换来的令牌" '"token": "enroll-token-1"' "$(cat "$SB/etc/agent.json")"
assert_eq "配置权限 600" "600" "$(stat -c %a "$SB/etc/agent.json")"
assert_absent "成功后无 .prev 二进制残留" "$SB/bin/ferry-agent.prev"
assert_absent "成功后无 .prev unit 残留" "$UNIT.prev"
assert_absent "成功后无 .new 残留" "$SB/bin/ferry-agent.new"
assert_contains "成功路径 restart 调用" "restart ferry-agent" "$(cat "$FAKE_LOG")"

# ---- 2. 升级成功：旧件被替换、.prev 清除 ----
: > "$FAKE_LOG"
echo old-binary > "$SB/bin/ferry-agent"
echo "old-unit" > "$UNIT"
run_install --register hk2 --enroll-token dummy --address hk2.example.com
assert_eq "升级成功退出码" 0 "$RC"
assert_eq "升级后二进制为新件" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"
assert_absent "升级成功后 .prev 清除" "$SB/bin/ferry-agent.prev"
assert_absent "升级成功后旧 unit 备份清除" "$UNIT.prev"
assert_eq "unit 被新内容覆盖" "1" "$(grep -c 'ExecStart=' "$UNIT")"

# ---- 3. 升级失败回滚：.prev 原样还原、退出非零、诊断可见 ----
: > "$FAKE_LOG"
echo old-binary-3 > "$SB/bin/ferry-agent"
echo "old-unit-3" > "$UNIT"
touch "$FAKE_FAIL/ferry-agent"
run_install --register hk3 --enroll-token dummy --address hk3.example.com
rm -f "$FAKE_FAIL/ferry-agent"
assert_eq "升级失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_eq "失败后二进制还原为旧件" "old-binary-3" "$(cat "$SB/bin/ferry-agent")"
assert_eq "失败后 unit 还原为旧件" "old-unit-3" "$(cat "$UNIT")"
assert_eq "回滚后再次 restart" "2" "$(grep -c 'restart ferry-agent' "$FAKE_LOG")"
assert_eq "回滚后 daemon-reload" "2" "$(grep -c 'daemon-reload' "$FAKE_LOG")"
assert_contains "失败诊断含 journalctl 采集" "fake-diagnostic" "$OUT"
assert_contains "失败信息提示已回滚" "回滚" "$OUT"
assert_eq "回滚后 .prev 保留供手动核对" "old-binary-3" "$(cat "$SB/bin/ferry-agent.prev")"

# ---- 4. 首装失败清理：无旧件时清掉本次产物，不留坏服务 ----
: > "$FAKE_LOG"
rm -f "$SB/bin/ferry-agent" "$UNIT" "$SB/bin/ferry-agent.prev"
touch "$FAKE_FAIL/ferry-agent"
run_install --register hk4 --enroll-token dummy --address hk4.example.com
rm -f "$FAKE_FAIL/ferry-agent"
assert_eq "首装失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_absent "首装失败后二进制已清理" "$SB/bin/ferry-agent"
assert_absent "首装失败后 unit 已清理" "$UNIT"
assert_contains "首装失败诊断可见" "fake-diagnostic" "$OUT"
assert_contains "首装失败提示已清理" "清理" "$OUT"

# ---- 5. 既有配置保留：重装不覆盖现场配置（原子写不落半截）----
: > "$FAKE_LOG"
run_install --register hk5 --enroll-token dummy --address hk5.example.com
assert_eq "带既有配置成功退出码" 0 "$RC"
assert_contains "既有配置未被覆盖" '"token": "enroll-token-1"' "$(cat "$SB/etc/agent.json")"
assert_absent "无 .new 配置残留" "$SB/etc/agent.json.new"

# ---- 6. QUIC sidecar 首装成功：agent+sidecar 双服务齐活、无 .prev 残留 ----
QUIC_UNIT="$SB/systemd/ferry-quic.service"
printf '#!/bin/sh\necho ferry-quic-fake\n' > "$SB/fake-quic"
chmod +x "$SB/fake-quic"
: > "$FAKE_LOG"
run_install --register hk6 --enroll-token dummy --address hk6.example.com \
  --quic --quic-binary "$SB/fake-quic" --quic-install "$SB/bin/ferry-quic"
assert_eq "sidecar 首装成功退出码" 0 "$RC"
assert_eq "sidecar 二进制落位" "echo ferry-quic-fake" "$(tail -1 "$SB/bin/ferry-quic")"
assert_eq "sidecar unit 已写" "1" "$(grep -c 'ExecStart=' "$QUIC_UNIT")"
assert_contains "sidecar restart 调用" "restart ferry-quic" "$(cat "$FAKE_LOG")"
assert_absent "sidecar 成功后无 .prev" "$SB/bin/ferry-quic.prev"
assert_absent "sidecar 成功后无 unit 备份" "$QUIC_UNIT.prev"

# ---- 7. QUIC sidecar 升级失败：sidecar 回滚到旧件，agent 不受牵连 ----
: > "$FAKE_LOG"
echo old-quic > "$SB/bin/ferry-quic"
echo "old-quic-unit" > "$QUIC_UNIT"
touch "$FAKE_FAIL/ferry-quic"
run_install --register hk7 --enroll-token dummy --address hk7.example.com \
  --quic --quic-binary "$SB/fake-quic" --quic-install "$SB/bin/ferry-quic"
rm -f "$FAKE_FAIL/ferry-quic"
assert_eq "sidecar 失败退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_eq "sidecar 二进制还原旧件" "old-quic" "$(cat "$SB/bin/ferry-quic")"
assert_eq "sidecar unit 还原旧件" "old-quic-unit" "$(cat "$QUIC_UNIT")"
assert_eq "agent 未被牵连（新件在位）" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"
assert_eq "sidecar 回滚后再次 restart" "2" "$(grep -c 'restart ferry-quic' "$FAKE_LOG")"
assert_contains "sidecar 失败提示已回滚" "回滚" "$OUT"

# ---- 8. 下载链路（FERRY-002）：latest 解析为实际版本再拼下载址，伴随 .sha256 校验 ----
: > "$FAKE_LOG"
rm -f "$SB/bin/ferry-agent" "$UNIT" "$SB/bin/ferry-agent.prev" "$UNIT.prev"
run_install_fetch --register hk8 --enroll-token dummy --address hk8.example.com
assert_eq "下载链路成功退出码" 0 "$RC"
assert_contains "latest 经 GitHub API 解析" "releases/latest" "$(cat "$FAKE_LOG")"
assert_contains "下载址用解析出的版本而非 vlatest" "/v0.9.1/ferry-agent_0.9.1_linux" "$(cat "$FAKE_LOG")"
assert_contains "伴随 .sha256 参与校验" "ferry-agent_0.9.1_linux_amd64.tar.gz.sha256" "$(cat "$FAKE_LOG")"
assert_eq "下载件解包后落位安装" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"

# ---- 9. QUIC 下载链路：--quic 不带 --quic-binary 时 sidecar 同走下载+校验 ----
: > "$FAKE_LOG"
run_install_fetch --register hk9 --enroll-token dummy --address hk9.example.com \
  --quic --quic-install "$SB/bin/ferry-quic"
assert_eq "QUIC 下载链路成功退出码" 0 "$RC"
assert_contains "sidecar 下载址用解析出的版本" "/v0.9.1/ferry-quic_0.9.1_linux" "$(cat "$FAKE_LOG")"
assert_contains "sidecar 伴随 .sha256 参与校验" "ferry-quic_0.9.1_linux_amd64.tar.gz.sha256" "$(cat "$FAKE_LOG")"
assert_eq "sidecar 下载件落位安装" "echo ferry-quic-fake" "$(tail -1 "$SB/bin/ferry-quic")"

# ---- 10. 尚无稳定版：latest 解析 404 立即失败，不拼 vlatest 假址、不落安装件 ----
: > "$FAKE_LOG"
rm -rf "$SB/bin10"
touch "$FAKE_FAIL/latest-404"
run_install_fetch --register hk10 --enroll-token dummy --address hk10.example.com \
  --install "$SB/bin10/ferry-agent" --config "$SB/etc/agent10.json"
rm -f "$FAKE_FAIL/latest-404"
assert_eq "无稳定版退出码非零" "1" "$([ "$RC" != 0 ] && echo 1 || echo 0)"
assert_contains "失败信息指向显式指定版本" "尚无正式稳定版发布" "$OUT"
assert_contains "失败前确曾查询 latest API" "releases/latest" "$(cat "$FAKE_LOG")"
case "$(cat "$FAKE_LOG")" in *vlatest*) bad "拼出了 vlatest 假地址" ;; *) ok "不拼 vlatest 假地址（失败在解析期）" ;; esac
assert_absent "失败后不落安装二进制" "$SB/bin10/ferry-agent"

echo "-----------------------------"
echo "pass=$pass fail=$fail"
[ "$fail" = 0 ]
