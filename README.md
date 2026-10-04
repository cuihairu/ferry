# ferry

ferry 是一个轻量级代理管理面板，面向小内存 VPS，提供用户、节点、订阅链接与流量记账管理。

## 目录结构

```
ferry/
├── apps/
│   ├── server/   # Go 后端（gin + 纯 Go SQLite，免 CGO）
│   └── web/      # Vue 3 前端（Vite + TypeScript + Pinia + Element Plus）
├── deploy/       # 部署文件（compose、脚本）
├── packages/     # 前后端共享的类型、常量与订阅编码
├── Makefile      # build / test / dev 统一入口
└── todo.md       # 功能拆解与排期
```

## 开发

待补充（Node 统一使用 24）。
