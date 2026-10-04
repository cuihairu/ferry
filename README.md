# ferry

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

## 开发

待补充（Node 统一使用 24）。
