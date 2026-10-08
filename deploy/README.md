# deploy

面板与服务端的容器部署物。

```sh
cp deploy/ferry.env.example deploy/ferry.env   # 按需修改
docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml up -d --build
```

- `ferry-server`：服务端单二进制，默认 sqlite 落卷 `/data`。
- `ferry-dash`：Caddy 托管面板静态产物，并反代 `/api`、`/sub`、`/agent`、`/feed.xml` 到服务端。
- `ferry-postgres`：可选（`--profile postgres`），切换存储见 `ferry.env.example` 注释。

agent 部署在节点上，不经此 compose（见《agent架构设计》）。

## dash 登录与管理员引导（安全设计 §1）

dash 全部页面走管理员登录（JWT），绑定两步验证后登录再验 6 位 TOTP/恢复码。
部署侧在 `ferry.env` 配 `FERRY_ADMIN_PASSWORD`：库内无管理员时启动按
`FERRY_ADMIN_USER`（缺省 admin）建号，已有管理员则忽略且不覆盖密码。
建议建号后进「设置 → 两步验证」绑定 TOTP（需先配 `FERRY_SECRET_KEY`）。

## agent 节点一键安装（A-24）

节点机 root 执行（或面板机上 `--register` 顺手建节点拿令牌）：

```sh
deploy/agent-install.sh --panel https://panel.example.com --token <节点令牌>
# 令牌还没建：
deploy/agent-install.sh --panel https://panel.example.com \
  --register hk1 --address hk1.example.com --port 443 --protocol vless
```

脚本做四件事：下载 `ferry-agent_<版本>_linux_<架构>.tar.gz` 并校验 sha256（`--binary` 可离线装）→
装到 `/usr/local/bin/ferry-agent` → 写 `/etc/ferry/agent.json`（已存在则保留）→
装 `ferry-agent.service` 并启动。日志：`journalctl -u ferry-agent -f`。

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

