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

## 目录结构

```
ferry/
├── server/     # Go 服务端（gin + 纯 Go SQLite，免 CGO）
├── agent/      # 节点代理（Go 静态二进制，常驻代理机，只出站连服务端）
├── panel/      # 用户面板（Vue 3：自助订阅链接、流量查询、使用说明）
├── dash/       # 管理后台（Vue 3：用户/节点/配置/统计看板）
├── payments/   # 支付渠道插件（epusdt USDT 已接入；微信/支付宝为占位，等商户资质）
├── packages/   # 共享契约（agentproto、支付 Provider 接口）
├── deploy/     # 部署文件（compose、脚本）
├── docs/       # 设计文档
├── todo.md     # 功能拆解与排期
└── Makefile    # build / test / dev 统一入口
```

## 合规定位与责任边界

ferry 是一个**代理集群管理工具**，部署与运营的合规责任在部署者。工具不提供代理服务本身、不运营网络接入、不内置默认目标；部署者对其所在辖区的法律法规与服务商条款的合规负责。详见 [docs/design/合规定位声明.md](docs/design/合规定位声明.md)。

同类面板调研（3x-ui / Marzban / Hiddify / Remnawave）见 [docs/面板横向对比.md](docs/面板横向对比.md)。

## 开发

```bash
make build   # 全量构建（server/agent/quic/dash/panel）
make test    # Go 测试（server/agent/packages/payments）+ 前端构建检查
make dev     # 本地联调（dev-server / dev-dash / dev-panel 可单跑）
make fmt     # gofmt
```

前端构建使用 Vite（dash 与 panel 均是）。Node 24 为开发机现用版本，非硬性要求——package.json 未声明 engines、仓内无 .nvmrc，Vite 支持的 Node 版本均可构建。环境变量配置项全集见 `server/internal/config/config.go`；HTTP API 以 `server/internal/handler/router.go` 的路由注册为准，各能力落地口径见 [docs/design/](docs/design/) 对应设计文档（文档末尾「落地口径」段）。
