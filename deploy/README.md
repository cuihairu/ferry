# deploy

面板与服务端的容器部署物。

```sh
cp deploy/ferry.env.example deploy/ferry.env   # 按需修改
docker compose --env-file deploy/ferry.env -f deploy/docker-compose.yml up -d --build
```

- `ferry-server`：服务端单二进制，默认 sqlite 落卷 `/data`。
- `ferry-dash`：Caddy 托管面板静态产物，并反代 `/api`、`/sub`、`/agent` 到服务端。
- `ferry-postgres`：可选（`--profile postgres`），切换存储见 `ferry.env.example` 注释。

agent 部署在节点上，不经此 compose（见《agent架构设计》）。

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

