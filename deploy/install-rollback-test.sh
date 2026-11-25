#!/usr/bin/env bash
# Sandbox regression of agent-install.sh's atomic replace / rollback / failure diagnostics (FERRY-005):
# never touches real systemd and needs no root — fakes (id/systemctl/journalctl/sleep) are put ahead on PATH,
# the binary comes via --binary, and install/config/unit directories all land in a temporary sandbox, while the
# script under test is the real one.
# Paths covered: first install success / upgrade success / upgrade-failure rollback / first-install failure
# cleanup / existing config preservation / QUIC sidecar success and upgrade-failure rollback / download path
# (FERRY-002: latest resolution, .sha256 verification alongside agent and quic, early failure when no stable release exists).
# CI wiring is in .github/workflows/test.yml's deploy-scripts job; run this file directly locally.
set -u
cd "$(dirname "$0")/.."

pass=0 fail=0
ok() { pass=$((pass+1)); echo "ok - $1"; }
bad() { fail=$((fail+1)); echo "FAIL - $1" >&2; }
assert_eq() { # $1=description $2=expected $3=actual
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1：want [$2] got [$3]"; fi
}
assert_contains() { # $1=description $2=substring expected $3=full text
  case "$3" in *"$2"*) ok "$1" ;; *) bad "$1：缺 [$2]" >&2 ;; esac
}
assert_absent() { # $1=description $2=path that must not exist
  if [ ! -e "$2" ]; then ok "$1"; else bad "$1：[$2] 不应存在"; fi
}

SB=$(mktemp -d)
trap 'rm -rf "$SB"' EXIT
mkdir -p "$SB/bin" "$SB/etc" "$SB/systemd" "$SB/fakebin"

# ---- Fakes: id reports root, systemctl records calls and simulates a start failure per $FAKE_FAIL/<service>
# (per-service control, so a sidecar-failed-with-agent-fine combination can be unit tested), journalctl emits a
# fixed diagnostic line, sleep no-ops (so wait polling costs no wall time), curl answers 201 on /api/enroll ----
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
  # show -p ActiveState/Result --value <unit>: the pair wait_service_active checks; when a fail marker simulates a
  # service start failure, it reports activating/exit-code (the real crash-loop shape)
  "show -p")
    if [ -f "$FAKE_FAIL/\$5" ]; then
      [ "\$3" = ActiveState ] && echo activating
      [ "\$3" = Result ] && echo exit-code
    else
      [ "\$3" = ActiveState ] && echo active
      [ "\$3" = Result ] && echo success
    fi
    exit 0 ;;
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
# Fake curl: /api/enroll writes the registration response into the -o file and reports 201; the GitHub API
# returns v0.9.1 (the latest-resolution path; a latest-404 marker makes it report 404, simulating no stable release
# yet); the tar.gz and .sha256 serve as pre-staged release artifacts (the .sha256 records the original long
# filename per the Release convention, exposing any mismatch of the comparison name)
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

# fake-agent: an executable placeholder (systemd is faked and will never really run it)
printf '#!/bin/sh\necho ferry-agent-fake\n' > "$SB/fake-agent"
chmod +x "$SB/fake-agent"

# Pre-staged release artifacts (feeding the download path): the binaries inside the packages are named
# ferry-agent/ferry-quic; per the GitHub Release convention the .sha256 records the original long filename, so the
# install script must rewrite the comparison name to the local download name (agent.tar.gz/quic.tar.gz) before verifying
mkdir -p "$SB/dl"
cp "$SB/fake-agent" "$SB/dl/ferry-agent"
printf '#!/bin/sh\necho ferry-quic-fake\n' > "$SB/dl/ferry-quic"
tar -czf "$SB/dl/ferry-agent_0.9.1_linux_amd64.tar.gz" -C "$SB/dl" ferry-agent
tar -czf "$SB/dl/ferry-quic_0.9.1_linux_amd64.tar.gz" -C "$SB/dl" ferry-quic
( cd "$SB/dl" && sha256sum ferry-agent_0.9.1_linux_amd64.tar.gz > agent.sha256 \
  && sha256sum ferry-quic_0.9.1_linux_amd64.tar.gz > quic.sha256 )

run_install() { # remaining arguments are passed through to the install script; results land in OUT/RC
  OUT=$(PATH="$SB/fakebin:$PATH" FERRY_SYSTEMD_UNIT_DIR="$SB/systemd" \
    bash deploy/agent-install.sh --panel https://panel.example.com \
    --binary "$SB/fake-agent" --install "$SB/bin/ferry-agent" \
    --config "$SB/etc/agent.json" "$@" 2>&1)
  RC=$?
}

run_install_fetch() { # without --binary: takes the download path (fake curl supplies artifacts); otherwise same as run_install
  OUT=$(PATH="$SB/fakebin:$PATH" FERRY_SYSTEMD_UNIT_DIR="$SB/systemd" \
    bash deploy/agent-install.sh --panel https://panel.example.com \
    --install "$SB/bin/ferry-agent" --config "$SB/etc/agent.json" "$@" 2>&1)
  RC=$?
}

UNIT="$SB/systemd/ferry-agent.service"
NEW_MARK='echo ferry-agent-fake'

# ---- 1. First install success: binary in place, config/unit complete, no .new/.prev leftovers ----
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

# ---- 2. Upgrade success: the old binary is replaced and .prev cleared ----
: > "$FAKE_LOG"
echo old-binary > "$SB/bin/ferry-agent"
echo "old-unit" > "$UNIT"
run_install --register hk2 --enroll-token dummy --address hk2.example.com
assert_eq "升级成功退出码" 0 "$RC"
assert_eq "升级后二进制为新件" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"
assert_absent "升级成功后 .prev 清除" "$SB/bin/ferry-agent.prev"
assert_absent "升级成功后旧 unit 备份清除" "$UNIT.prev"
assert_eq "unit 被新内容覆盖" "1" "$(grep -c 'ExecStart=' "$UNIT")"

# ---- 3. Upgrade-failure rollback: .prev restored as-is, non-zero exit, diagnostics visible ----
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

# ---- 4. First-install failure cleanup: with no previous binary, this run's artifacts are removed, leaving no broken service ----
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

# ---- 5. Existing config preserved: a reinstall never overwrites on-site config (atomic writes never leave a half-written file) ----
: > "$FAKE_LOG"
run_install --register hk5 --enroll-token dummy --address hk5.example.com
assert_eq "带既有配置成功退出码" 0 "$RC"
assert_contains "既有配置未被覆盖" '"token": "enroll-token-1"' "$(cat "$SB/etc/agent.json")"
assert_absent "无 .new 配置残留" "$SB/etc/agent.json.new"

# ---- 6. QUIC sidecar first install success: both agent and sidecar services up, no .prev leftovers ----
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

# ---- 7. QUIC sidecar upgrade failure: the sidecar rolls back to the old binary and the agent is unaffected ----
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

# ---- 8. Download path (FERRY-002): latest resolves to the real version before the URL is assembled, with a companion .sha256 check ----
: > "$FAKE_LOG"
rm -f "$SB/bin/ferry-agent" "$UNIT" "$SB/bin/ferry-agent.prev" "$UNIT.prev"
run_install_fetch --register hk8 --enroll-token dummy --address hk8.example.com
assert_eq "下载链路成功退出码" 0 "$RC"
assert_contains "latest 经 GitHub API 解析" "releases/latest" "$(cat "$FAKE_LOG")"
assert_contains "下载址用解析出的版本而非 vlatest" "/v0.9.1/ferry-agent_0.9.1_linux" "$(cat "$FAKE_LOG")"
assert_contains "伴随 .sha256 参与校验" "ferry-agent_0.9.1_linux_amd64.tar.gz.sha256" "$(cat "$FAKE_LOG")"
assert_eq "下载件解包后落位安装" "$NEW_MARK" "$(tail -1 "$SB/bin/ferry-agent")"

# ---- 9. QUIC download path: --quic without --quic-binary sends the sidecar down the same download+verify path ----
: > "$FAKE_LOG"
run_install_fetch --register hk9 --enroll-token dummy --address hk9.example.com \
  --quic --quic-install "$SB/bin/ferry-quic"
assert_eq "QUIC 下载链路成功退出码" 0 "$RC"
assert_contains "sidecar 下载址用解析出的版本" "/v0.9.1/ferry-quic_0.9.1_linux" "$(cat "$FAKE_LOG")"
assert_contains "sidecar 伴随 .sha256 参与校验" "ferry-quic_0.9.1_linux_amd64.tar.gz.sha256" "$(cat "$FAKE_LOG")"
assert_eq "sidecar 下载件落位安装" "echo ferry-quic-fake" "$(tail -1 "$SB/bin/ferry-quic")"

# ---- 10. No stable release yet: latest resolving to 404 fails immediately, assembling no fake vlatest URL and installing nothing ----
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
