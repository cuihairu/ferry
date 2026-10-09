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

ferry is a lightweight proxy management panel for small-memory VPS, covering users, nodes, subscription links, and traffic accounting.

Documentation: [https://cuihairu.github.io/ferry/](https://cuihairu.github.io/ferry/) — design documents, comparison and consistency audit.

## Repository Layout

```
ferry/
├── server/     # Go backend (gin + pure-Go SQLite, no CGO)
├── agent/      # Node agent (static Go binary, resident on proxy machines, outbound-only to the server)
├── panel/      # User portal (Vue 3: self-service subscription links, traffic lookup, usage instructions)
├── dash/       # Admin dashboard (Vue 3: users/nodes/configuration/statistics)
├── payments/   # Payment channel plugins (epusdt USDT integrated; WeChat/Alipay are placeholders pending merchant credentials)
├── packages/   # Shared contracts (agentproto, payment provider interfaces)
├── deploy/     # Deployment files (compose, scripts)
├── docs/       # Design documents
├── todo.md     # Feature breakdown and scheduling
└── Makefile    # Unified entry point for build / test / dev
```

## Compliance Positioning and Responsibility Boundary

ferry is a **proxy cluster management tool**; compliance responsibility for deployment and operation rests with the deployer. The tool does not provide proxy services itself, does not operate network access, and ships with no default destinations. Deployers are responsible for compliance with the laws and regulations of their jurisdiction and with their service providers' terms of service. See [docs/design/合规定位声明.md](docs/design/合规定位声明.md) (compliance positioning statement, in Chinese).

A survey of comparable panels (3x-ui / Marzban / Hiddify / Remnawave) is available at [docs/面板横向对比.md](docs/面板横向对比.md) (in Chinese).

## Development

```bash
make build   # Full build (server/agent/quic/dash/panel)
make test    # Go tests (server/agent/packages/payments) + frontend build checks
make dev     # Local development (dev-server / dev-dash / dev-panel can run individually)
make fmt     # gofmt
```

Frontend builds use Vite (both dash and panel). Node 24 is what dev machines currently run, not a hard requirement — `package.json` declares no `engines` field and the repo has no `.nvmrc`, so any Node version Vite supports works. The full set of environment variables is documented in `server/internal/config/config.go`; the HTTP API is defined by the route registrations in `server/internal/handler/router.go`. Implementation details for each capability are recorded in the corresponding design documents under [docs/design/](docs/design/) (see the "落地口径" implementation-notes section at the end of each document, in Chinese).
