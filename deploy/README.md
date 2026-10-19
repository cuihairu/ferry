# deploy

面板与服务端的容器部署物。

## 一键启动

```sh
./deploy/start.sh                     # 等价：make up
./deploy/start.sh --profile postgres  # 同时起 postgres 存储
```

`start.sh` 首次运行从仓根 `.env.example` 生成 `.env`（已存在不覆盖），构建起
服务、最多等 180 秒 `/api/health` 就绪，并校验库文件落盘；打印的地址取 compose
实际映射端口。前置要求、常见问题见根 README「一键启动」节。

按老口径手起（等价，只是不做就绪与建库校验）：

```sh
cp .env.example .env                  # 或沿用 ferry.env.example 的全量参数集
docker compose --env-file .env -f deploy/docker-compose.yml up -d --build
```

- `ferry-server`：服务端单二进制，默认 sqlite 落卷 `/data`，启动即 `AutoMigrate` 建表（幂等，可重复执行）。
- `ferry-dash`：Caddy 托管管理端静态产物，并反代 `/api`、`/sub`、`/agent`、`/feed.xml` 到服务端；端口 `${FERRY_HTTP_PORT:-8080}`。
- `ferry-panel`：Caddy 托管用户端静态产物，反代规则同上；端口 `${FERRY_PANEL_PORT:-8081}`。
- `ferry-postgres`：可选（`--profile postgres`），切换存储见 `.env.example` / `ferry.env.example` 注释。

`payments/` 是编译进 `ferry-server` 的 Go 库（无 `main`），不经此 compose。

## compose 真部署冒烟

```sh
bash deploy/compose-smoke.sh    # 43 断言，~3 分钟；CI 走 nightly.yml compose-smoke job
```

随机 compose project 名 + `down -v` 清容器与卷，跑两条腿，绝不碰本机面板数据：

- sqlite 腿：build + up 三服务，经 **Caddy 前端**（不是容器直连）验证
  `/api/health` 反代、dash/panel 静态产物、SPA history 深链回退 index.html、
  顶层前缀反代（`POST /admin/login` 返回 401 JSON 而非 405，专门打 Caddyfile
  漏登记会被 `file_server` 兜底成「能开页面但登不上」的那种故障）、
  `/feed.xml`、`/distributor/login`、备份导出与 `backup.fresh` 记账、
  teardown 后端口释放与卷删除；
- postgres 腿（`--profile postgres`）：compose 记载但此前无自动化覆盖的
  存储切换路径，验证四服务运行、迁移在 postgres 上完成、登录与 JWT，
  以及 `FERRY_KEYFILE=/data/ferry-keys.env` 生效后密钥落数据卷 0600、
  **重启后密钥文件不变**（postgres 存储下不配该变量即密钥每轮重启换掉，
  旧 JWT 全废、已加密备份解不开）。

`start.sh --profile postgres` 的参数装配（`--profile` 是 compose 全局参数，
必须排在子命令前）与 postgres 缺 `FERRY_PG_PASSWORD` 的早失败有回归测试：

```sh
bash deploy/start-args-test.sh    # 8 断言，PATH 假 docker，不起真容器
```

agent 部署在节点上，不经此 compose（见《agent架构设计》）。

## dash 登录与管理员引导（安全设计 §1）

dash 全部页面走管理员登录（JWT），绑定两步验证后登录再验 6 位 TOTP/恢复码。
部署侧在 `ferry.env` 配 `FERRY_ADMIN_PASSWORD`：库内无管理员时启动按
`FERRY_ADMIN_USER`（缺省 admin）建号，已有管理员则忽略且不覆盖密码。
建议建号后进「设置 → 两步验证」绑定 TOTP（需先配 `FERRY_SECRET_KEY`）。

## agent 节点一键安装（A-24）

节点机 root 执行（节点令牌已有用 `--token`，没有走一次性注册凭据，FERRY-001）：

```sh
deploy/agent-install.sh --panel https://panel.example.com --token <节点令牌>
# 节点令牌还没建——面板机上先签发一次性注册凭据（60 分钟单次有效，可吊销）：
curl -X POST https://panel.example.com/api/enrollment-tokens \
  -H "Authorization: Bearer <管理员 JWT 或 API Token>" -d '{"note":"hk1 首装"}'
# 再在节点机用 --register + --enroll-token 注册并安装：
deploy/agent-install.sh --panel https://panel.example.com \
  --register hk1 --enroll-token <注册凭据> \
  --address hk1.example.com --port 443 --protocol vless
```

脚本做四件事：下载 `ferry-agent_<版本>_linux_<架构>.tar.gz` 并校验 sha256（`--binary` 可离线装）→
装到 `/usr/local/bin/ferry-agent` → 写 `/etc/ferry/agent.json`（已存在则保留）→
装 `ferry-agent.service` 并启动。日志：`journalctl -u ferry-agent -f`。

失败恢复（FERRY-005）：二进制与 unit 均先备份旧件（`.prev`）再原子替换，
配置走临时文件+rename 不落半截；新件启动失败自动回滚到变更前状态并把
journal 最近日志打在终端，成功则清掉备份。`--config`/`--install` 落点随路径建目录。

配置校验口径：`meta.bw_up_mbps`/`meta.bw_down_mbps` 是 agent 配置必填项，
脚本写 `--bw-up`/`--bw-down`（默认 100/100 Mbps，面板节点页可改）；
`--node-role entry|landing|both` 写集群角色（不写时 agent 侧默认 landing）。

## relay 节点安装（E-5/E-33）

`--role relay` 让 agent 以 `-role relay` 独立进程起 relay 数据面
（本机监听 → 经传输插件拨落地；面板 config.push 可热重指落地参数）：

```sh
# tls-camo（默认传输）
deploy/agent-install.sh --panel ... --token ... --role relay \
  --relay-landing land.example.com:443

# SSH 传输（备选·特征独特，publickey 单一认证；密钥是本机文件路径，不进面板）
deploy/agent-install.sh --panel ... --token ... --role relay \
  --relay-landing land.example.com:22 --relay-tunnel ssh \
  --relay-server-name land.example.com:22 \
  --relay-ssh-user root --relay-ssh-key /root/.ssh/id_ed25519 \
  --relay-ssh-known-hosts /root/.ssh/known_hosts \
  --relay-ssh-forward-addr 127.0.0.1:1080
```

传输插件：`tls-camo`（默认）/ `quic` / `ws-tls` / `ssh`；
`--relay-listen`（默认 127.0.0.1:1080）、`--relay-ca`（私有 CA 校验落地证书）可配。
tunnel=ssh 时 `--relay-server-name` 指落地 sshd 地址、`--relay-ssh-user`/`--relay-ssh-key`/`--relay-ssh-forward-addr` 三件必填，known-hosts 可空（空=跳过 host key 校验，仅引导期）。

## QUIC sidecar 安装（E-18b）

`--quic` 额外装独立二进制 `ferry-quic` 并起 `ferry-quic.service`
（client 模式：本机 TCP 桥 → QUIC 拨落地；raw QUIC，ALPN `ferry-quic`，
不是 hysteria2）。不用 QUIC 传输的部署不装：

```sh
deploy/agent-install.sh --panel ... --token ... --role relay \
  --relay-landing land.example.com:443 --relay-tunnel quic --quic
```

`--quic-listen`（默认 127.0.0.1:7300，与 agent 内置 quic 插件缺省对齐，
非默认时自动写 `FERRY_QUIC_SIDECAR` 环境变量）、`--quic-ca`、
`--quic-insecure`（仅引导调试）、`--quic-binary`（离线装）、
`--quic-install`（sidecar 落盘路径，默认 /usr/local/bin/ferry-quic）。

## 被管内核进程（procs[]）与 hysteria2 内核（hy2 批）

agent.json `procs[]` 声明本机被管代理进程（路径可配，按需增删）。
**二进制必须自行安装**（口径：二进制适配/进程隔离，ferry 不 import
xray-core/sing-box/hysteria2 源码）；agent 启动时预检 exec，缺失即发
`proc_crash` 告警（面板节点页可见明确原因），装上后自动拉起：

```json
{
  "procs": [
    {
      "name": "xray", "kind": "xray",
      "exec": "/usr/local/bin/xray", "args": ["run", "-c", "/etc/ferry/xray/config.json"],
      "config_path": "/etc/ferry/xray/config.json",
      "asset_dir": "/etc/ferry/xray/assets",
      "stats_api": "127.0.0.1:10085", "reload": "restart",
      "validate": "/usr/local/bin/xray run -test -config {config}"
    },
    {
      "name": "hysteria2", "kind": "hysteria2",
      "exec": "/usr/local/bin/hysteria", "args": ["server", "-c", "/etc/ferry/hy2/config.yaml"],
      "config_path": "/etc/ferry/hy2/config.yaml",
      "reload": "restart"
    },
    {
      "name": "sing-box", "kind": "sing-box",
      "exec": "/usr/local/bin/sing-box", "args": ["run", "-c", "/etc/ferry/sing-box/config.json"],
      "config_path": "/etc/ferry/sing-box/config.json",
      "reload": "restart",
      "validate": "/usr/local/bin/sing-box check -c {config}"
    }
  ]
}
```

要点：

- `exec` 即二进制路径（可配）；hysteria2 官方发布件无配置校验子命令，
  `validate` 留空则 agent 跳过校验（下发改 reload=restart 生效，失败回滚）；
- hy2 内核监听 **UDP 443**（QUIC/HTTP-3 同形伪装），放行防火墙 UDP 443；
  证书路径按 acme.sh/certbot 实际路径改；
- hy2 server 配置示例（字段与 hysteria2 server 二进制同口径，YAML）：

```yaml
listen: :443

tls:
  cert: /etc/ferry/hy2/fullchain.pem
  key: /etc/ferry/hy2/privkey.pem

auth:
  type: password
  password: <change-me>

bandwidth:
  up: 100 mbps
  down: 200 mbps

masquerade:
  type: proxy
  proxy:
    url: https://news.ycombinator.com/
    rewriteHost: true
```

- 订阅侧出口：`/sub/:token?target=singbox` 出 sing-box outbounds 数组
  （五协议全覆盖，SB 批）；clash/mihomo 订阅与 v2ray 链接里 hy2 节点自动为
  `type: hysteria2` / `hysteria2://` 条目（节点配置模板 JSON 写
  password/sni/obfs/up/down）。
- 许可：sing-box GPL-3.0、hysteria2 AGPL-3.0——只做二进制适配（进程隔离
  托管 + 配置下发），无源码链接，copyleft 不传染 ferry（见
  [开源选型设计](../docs/design/开源选型设计.md) 与 THIRD_PARTY.md）。

## mTLS 证书生成（A-24）

面板机自签 CA 并按节点签发客户端证书（口径见《安全设计》§2.2，CA 私钥只留面板机）：

```sh
deploy/mtls-ca.sh init                # 生成 deploy/mtls/ca.{crt,key}
deploy/mtls-ca.sh issue hk1 825       # 签发 hk1.crt/.key，打印 agent 侧三件套路径
```

安装时把三个路径传给安装脚本即可启用双因子：

```sh
deploy/agent-install.sh --panel ... --token ... \
  --ca deploy/mtls/ca.crt --cert deploy/mtls/hk1.crt --key deploy/mtls/hk1.key
```

## 一键恢复与迁移（面板可用性 §3/§4）

```sh
deploy/restore.sh --backup ferry-backup-*.db.enc --data-dir /data \
  --ferry-bin /usr/local/bin/ferry-server --key "$FERRY_SECRET_KEY" \
  --start-cmd "systemctl restart ferry" --health-url http://127.0.0.1:8080/api/health
```

六步：部署检查→解密（`.enc` 经 ferry 自身 `backup-decrypt` 子命令，openssl 无
AES-GCM 能力）→导入（SQLite 留底+原子覆盖 / `--pg-dsn` 走 pg_restore）→证书
目录→启动→健康冒烟；缺参/解密失败明确报错非 0，可复跑幂等。
换机迁移五步操作化（DNS 切换、agent 自动重连核对、回滚）见
[MIGRATION-RUNBOOK.md](MIGRATION-RUNBOOK.md)。

## 面板主从同步（P2-1）

单面板零配置零变化。需要第二台 VPS 做热备时：

主面板（只加一个令牌开放快照端点）：

```sh
# ferry.env
FERRY_STANDBY_TOKEN=<openssl rand -hex 32>
```

standby 实例（第二台 VPS，同一镜像；令牌与主密钥与主面板一致）：

```sh
# ferry.env
FERRY_STANDBY_TOKEN=<同一令牌>
FERRY_STANDBY_MASTER_URL=https://<主面板地址>
FERRY_STANDBY_SEC=600
# FERRY_SECRET_KEY 必须与主面板一致（解密校验快照）
```

语义：standby 每 `FERRY_STANDBY_SEC` 秒拉一次主面板加密快照（VACUUM INTO
在线一致性快照，与周期备份同格式，restore.sh 也可直接恢复），主密钥解密
校验后原子替换本机库（先静默连接再换文件并清旧 `-wal/-shm` 侧车），随后
进程退出交 systemd/compose 重启加载新库——RPO ≤ 拉取间隔；主面板不可达
或校验失败时不替换，继续服务陈旧库。提升为主：去掉 `MASTER_URL` 重启，
配回主面板地址即接管流量。

