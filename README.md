<p align="center">
  <img src="docs/public/logo.svg" width="64" alt="ferry logo" />
</p>

<h1 align="center">ferry</h1>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white" alt="Go 1.27" />
  <img src="https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white" alt="Vue 3" />
  <img src="https://img.shields.io/badge/SQLite-PG-MySQL-336791?logo=postgresql&logoColor=white" alt="SQLite / PostgreSQL / MySQL" />
  <img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="Apache-2.0" />
</p>

ferry 是一个轻量级代理管理面板，面向小内存 VPS，提供用户、节点、订阅链接与流量记账管理。

## 目录结构

```
ferry/
├── server/     # Go 服务端（gin + 纯 Go SQLite，免 CGO）
├── agent/      # 节点代理（Go 静态二进制，常驻代理机，只出站连服务端）
├── panel/      # 用户面板（Vue 3：自助订阅链接、流量查询、使用说明）
├── dash/       # 管理后台（Vue 3：用户/节点/配置/统计看板）
├── payments/   # 支付网关插件（卡密、USDT、商户直连预留）
├── packages/   # 共享契约（agentproto、支付 Provider 接口）
├── deploy/     # 部署文件（compose、脚本）
├── docs/       # 设计文档
├── todo.md     # 功能拆解与排期
└── Makefile    # build / test / dev 统一入口
```

## 合规定位与责任边界

ferry 是一个**代理集群管理工具**，部署与运营的合规责任在部署者。工具不提供代理服务本身、不运营网络接入、不内置默认目标；部署者对其所在辖区的法律法规与服务商条款的合规负责。详见 [docs/design/合规定位声明.md](docs/design/合规定位声明.md)。

## 开发

待补充（Node 统一使用 24）。
