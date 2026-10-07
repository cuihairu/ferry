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
