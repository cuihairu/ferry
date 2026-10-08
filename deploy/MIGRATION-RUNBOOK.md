# 面板迁移 Runbook

把 ferry 面板从一台机器迁到另一台（或同机重装）。目标：**节点侧零操作**——
agent 只出站连面板，面板地址不变（或 DNS 已切换）即自动回池；订阅域名不变则
用户无感。设计口径：《面板可用性设计》§3（一键恢复）/ §4（迁移五步）。

## 前置清单

- [ ] 新机已装 Docker 与 compose 插件；
- [ ] 新机 ferry 版本 ≥ 旧机（同版本或更高，恢复流程 ① 的口径）；
- [ ] 主密钥 `FERRY_SECRET_KEY` 在手——**主密钥不进备份包**（见《安全设计》§3），
      丢了它 `.enc` 备份档解不开；
- [ ] 证书材料位置已知：mTLS CA（`deploy/mtls/`，`mtls-ca.sh` 产物）、证书编排
      工作目录（`FERRY_ACME_HOME`，缺省 `/var/lib/ferry/acme`）；面板 Web 证书
      随库（settings 表），不用单独搬；
- [ ] DNS 面板域名 TTL 已提前调低（建议前一天 300s→60s，见步骤三）；
- [ ] （可选）预告维护窗口：迁移期间购买/兑换/注册暂停——面板挂 = 管理与商业化
      暂停，不是服务中断（设计 §1），存量订阅不受影响。

## 步骤一：旧面板手动备份

dash 备份入口一键下载（推荐，落档即留痕 `backups` 行），或直接调端点：

```sh
curl -H "Authorization: Bearer $ADMIN_JWT" -o ferry-backup-migrate.db \
  https://旧面板域名/admin/backup/db
```

配了主密钥的部署下载体是 `.enc` 加密档，未配是明文 `.db`。

**验收**：

```sh
ls -la ferry-backup-migrate.db            # 非空
head -c 15 ferry-backup-migrate.db        # 明文档应看到 "SQLite format 3"
```

顺带把证书材料拷下来：

```sh
scp -r 旧机:/var/lib/ferry/acme /tmp/migrate-acme        # 若用了证书编排
scp -r 旧机:ferry/deploy/mtls  /tmp/migrate-mtls         # 若用了 mTLS
```

## 步骤二：新机部署 + 恢复

```sh
git clone https://github.com/cuihairu/ferry.git && cd ferry
cp deploy/ferry.env.example deploy/ferry.env    # FERRY_SECRET_KEY 填旧机同款主密钥
docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml up -d --build
docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml stop ferry-server
scp 旧机:ferry-backup-migrate.db* /tmp/
```

恢复用 `deploy/restore.sh`（六步：部署检查→解密→导入→证书→启动→健康冒烟，
详见脚本头注释）。compose 部署的数据卷在宿主机的路径（compose 项目名=目录名）：

```sh
DATA=/var/lib/docker/volumes/ferry_ferry-data/_data

# 解密需要一个 ferry-server 二进制（openssl 不支持备份档的 AES-GCM 格式），
# 本机现编一个或取同版本 release 产物：
cd server && go build -o /tmp/ferry-server ./cmd/ferry && cd ..

deploy/restore.sh \
  --backup /tmp/ferry-backup-migrate.db.enc \
  --ferry-bin /tmp/ferry-server \
  --key "$(grep -E '^FERRY_SECRET_KEY=' deploy/ferry.env | cut -d= -f2-)" \
  --data-dir "$DATA" \
  --cert-src /tmp/migrate-acme --cert-dst /var/lib/ferry/acme \
  --start-cmd "docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml start ferry-server" \
  --health-url "http://127.0.0.1:${FERRY_HTTP_PORT:-8080}/api/health"
```

- 明文 `.db` 档去掉 `--key` 与 `--ferry-bin` 即可（解密步自动跳过）；
- PG 部署改传 `--pg-dsn`，档须自备 `pg_dump -Fc` 产物（本仓周期备份只产
  SQLite 档，PG 走各自备份设施，见设计 §2.2）；
- 证书目录按新机实际口径放置，路径与旧机不一致时用 `--cert-src/--cert-dst` 搬。

**验收**：脚本逐步打出 ①-⑥ 且健康冒烟打印出 `"status":"ok"`（或 `"degraded"`
——检查项异常如实上报，看 body 里哪项不新鲜）；dash 能登录、节点/用户/订单
数据与旧机一致；`$DATA/ferry.db.pre-restore-<ts>` 留底存在。

## 步骤三：DNS 切换

- **前一天**：面板域名（及订阅独立域名，若有）TTL 调低，如 300→60；
- **切换**：A/AAAA 记录改指新机 IP：

```sh
dig +short panel.example.com                       # 已解析到新机 IP
curl -s https://panel.example.com/api/health | head -c 200
```

同机重装不涉及本步。

## 步骤四：agent 自动重连核对

agent 只出站连面板、无监听，面板地址不变/DNS 生效后按重连退避自动回池
（心跳周期缺省 30s），**节点侧零操作**。

```sh
curl -s https://panel.example.com/api/health | grep -o '"agents_online":[0-9]*'
```

**验收**：在线数恢复到迁移前水平，dash 节点页逐台 `online`。

个别节点长时间不回来：登该节点 `journalctl -u ferry-agent -n 50`——最常见原因
是 `agent.json` 里 panel 地址写的是旧机 **IP** 而非域名，IP 部署的节点需手动
改为新地址再重启 agent；域名部署的节点等 DNS 与退避即可。

订阅域名确认：订阅链接与面板同域名（Caddy `/sub` 反代）时用户无感；订阅用了
独立域名的，该域名 DNS 与步骤三一并切。

## 步骤五：旧机下线与对接地址更新

- 观察满一个备份周期（缺省每日）+ 节点全部在线后，停旧机面板并**保留卷数日**：

```sh
docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml down
```

- Herald 对接地址若变更：dash 告警通道配置更新（`FERRY_HERALD_URL`，HERALD-2）；
- servify/其他外部对接同理，指到新地址；
- 停掉旧机上的备份定时任务（避免旧机继续落档造成两份真相）；
- 数日后清理旧机卷与证书材料。

## 回滚

确认期内发现新机异常：DNS 切回旧机 IP（TTL 已提前调低，分钟级生效），旧机
面板未下线即自动接管，agent 随 DNS 回切。**因此旧机在确认期不要销毁。**
新机库被污染需要重来：restore.sh 幂等可复跑（每次留底现有库），修好源备份档
再跑一遍即可。

---

> 合规定位：ferry 是代理集群管理工具，部署与运营的合规责任在部署者（见《合规定位声明》）。
