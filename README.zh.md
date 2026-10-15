[English](README.md) | [中文](README.zh.md)

<p align="center">
  <img src="docs/public/logo.svg" width="64" alt="ferry logo" />
</p>

<h1 align="center">ferry</h1>

<p align="center">
  <img src="docs/public/badges/go.svg" alt="Go 1.27" />
  <img src="docs/public/badges/vue.svg" alt="Vue 3" />
  <img src="docs/public/badges/db.svg" alt="SQLite / PostgreSQL / MySQL" />
  <img src="https://codecov.io/gh/cuihairu/ferry/branch/main/graph/badge.svg" alt="codecov" />
  <img src="docs/public/badges/license.svg" alt="Apache-2.0" />
</p>

ferry 是一个轻量级代理管理面板，面向小内存 VPS，提供用户、节点、订阅链接与流量记账管理。

在线文档：[https://cuihairu.github.io/ferry/](https://cuihairu.github.io/ferry/)（设计文档、横向对比与一致性审计）。

## 一键启动（Docker Compose）

前置要求：Docker + Compose 插件（`docker compose version`）。其余都不需要——Go、Node、pnpm 全在构建容器里跑，宿主机不用装。

```bash
git clone https://github.com/cuihairu/ferry.git && cd ferry
cp .env.example .env     # 改端口 / 密钥（本机试跑可跳过）
./deploy/start.sh        # 或：make up
```

`start.sh` 在 `.env` 不存在时从 `.env.example` 生成（已存在的 `.env` 绝不覆盖），构建镜像、起全部服务，最多等 180 秒 `/api/health` 就绪，再校验数据库文件是否真的落盘。改用 PostgreSQL：同一命令追加 `--profile postgres`，并把 `.env` 里的 `FERRY_DB_DRIVER` / `FERRY_DB_DSN` / `FERRY_PG_PASSWORD` 三行取消注释。

| 入口 | 地址 |
| --- | --- |
| 管理后台 | http://localhost:8080/ |
| 用户面板 | http://localhost:8081/ |
| 接口健康 | http://localhost:8080/api/health |

后台首次登录跑管理员引导：库内无管理员行时按 `.env` 的 `FERRY_ADMIN_USER` / `FERRY_ADMIN_PASSWORD` 建号（用户名缺省 `admin`）。建表不需手工步骤——`storage.Open` 每次启动跑 `AutoMigrate`，幂等可重复，对已有卷重跑 `start.sh` 是安全的。已有库迁入见 [deploy/MIGRATION-RUNBOOK.md](deploy/MIGRATION-RUNBOOK.md)。

常见问题：

- **端口占用** —— `up` 会报 bind 错误。改 `.env` 里的 `FERRY_HTTP_PORT` 或 `FERRY_PANEL_PORT` 后重跑 `start.sh`；脚本会打印实际映射到的端口，不用猜。
- **服务端起不来 / 库没建出来** —— `start.sh` 会打出最近 100 行日志并以非零码退出。建库与迁移路径见 [deploy/MIGRATION-RUNBOOK.md](deploy/MIGRATION-RUNBOOK.md)；改存储配置前先 `docker compose --env-file .env -f deploy/docker-compose.yml down` 停掉。
- **凭据没配** —— `.env` 把安全相关项默认注释掉了。`FERRY_SECRET_KEY` 为空表示云 API 密钥等机密不加密封存；`FERRY_ADMIN_SECRET` / `FERRY_API_SECRET` 为空回落到开发期常量，本机可接受、公网不行。用 `openssl rand -hex 32` 生成。
- **容器名与另一份检出冲突** —— compose 用 compose 文件所在目录名当项目名，本仓前缀是 `deploy-`（`deploy-ferry-server-1` 等）。同一 Docker daemon 上任何把 compose 文件放在 `deploy/` 目录的检出或仓库都用同一项目名。给每条命令加 `-p <名字>` 消歧：`docker compose -p ferry-prod --env-file .env -f deploy/docker-compose.yml up -d`。共享 daemon 上不要加 `--remove-orphans`——它会删掉 compose 误认成本项目孤儿的其它项目容器。

**compose 里跑什么：** `ferry-server`（Go 接口，默认 sqlite）、`ferry-dash`（管理后台）、`ferry-panel`（用户面板）、`ferry-postgres`（仅 `--profile postgres`）。

**刻意不进 compose 的：** `payments/` 是编译进 `ferry-server` 的 Go 库——没有 `main`，无可独立运行的进程。`agent/` 是装在每台代理节点上的二进制，由 [`deploy/agent-install.sh`](deploy/agent-install.sh) 安装：它向面板出站连接并以 systemd 服务常驻客户机，属节点侧，不属面板部署。

## 目录结构

```
ferry/
├── server/     # Go 服务端（gin + 纯 Go SQLite，免 CGO）
├── agent/      # 节点代理（Go 静态二进制，常驻代理机，只出站连服务端）
├── panel/      # 用户面板（Vue 3：自助订阅链接、流量查询、使用说明）
├── dash/       # 管理后台（Vue 3：用户/节点/配置/统计看板）
├── payments/   # 支付渠道插件（epusdt USDT 已接入；微信/支付宝为占位，等商户资质）
├── packages/   # 共享契约（agentproto、支付 Provider 接口）
├── deploy/     # 部署文件（compose、Caddy、一键启动 start.sh、迁移手册）
├── docs/       # 设计文档
├── todo.md     # 功能拆解与排期
└── Makefile    # build / test / dev 统一入口
```

## 合规定位与责任边界

ferry 是一个**代理集群管理工具**，部署与运营的合规责任在部署者。工具不提供代理服务本身、不运营网络接入、不内置默认目标；部署者对其所在辖区的法律法规与服务商条款的合规负责。详见 [docs/design/合规定位声明.md](docs/design/合规定位声明.md)。

代理引擎（Xray-core MPL-2.0、sing-box GPL-3.0-or-later、hysteria2 AGPL-3.0）**只做二进制适配**：agent 以外部进程托管（启停/重载/配置下发），不 import 源码、不链接，copyleft 不传染 ferry；引擎二进制由部署者自行安装。完整清单见 [THIRD_PARTY.md](THIRD_PARTY.md)。

同类面板调研（3x-ui / Marzban / Hiddify / Remnawave）见 [docs/面板横向对比.md](docs/面板横向对比.md)。

## 开发

```bash
make build   # 全量构建（server/agent/quic/dash/panel）
make test    # Go 测试（server/agent/packages/payments）+ 前端构建检查
make dev     # 本地联调（dev-server / dev-dash / dev-panel 可单跑）
make up      # 一键容器启动（./deploy/start.sh 等价入口）
make fmt     # gofmt
```

前端构建使用 Vite（dash 与 panel 均是）。Node 24 为开发机现用版本，非硬性要求——package.json 未声明 engines、仓内无 .nvmrc，Vite 支持的 Node 版本均可构建。环境变量配置项全集见 `server/internal/config/config.go`；HTTP API 以 `server/internal/handler/router.go` 的路由注册为准，各能力落地口径见 [docs/design/](docs/design/) 对应设计文档（文档末尾「落地口径」段）。
