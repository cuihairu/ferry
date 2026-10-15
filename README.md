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

## One-Click Startup (Docker Compose)

Prerequisites: Docker with the Compose plugin (`docker compose version`). Nothing else — Go, Node and pnpm all run inside the build containers, so the host does not need them.

```bash
git clone https://github.com/cuihairu/ferry.git && cd ferry
cp .env.example .env     # edit ports / secrets (skippable for local trials)
./deploy/start.sh        # or: make up
```

`start.sh` copies `.env.example` to `.env` when `.env` is missing (an existing `.env` is never overwritten), builds the images, starts every service, waits up to 180 s for `/api/health`, then checks that the database file was actually created. For a PostgreSQL backend, add `--profile postgres` to the same command and uncomment the `FERRY_DB_DRIVER` / `FERRY_DB_DSN` / `FERRY_PG_PASSWORD` lines in `.env`.

| What | URL |
| --- | --- |
| Admin dashboard | http://localhost:8080/ |
| User panel | http://localhost:8081/ |
| API health | http://localhost:8080/api/health |

The first dashboard login runs the admin bootstrap: when the database has no admin row it creates one from `FERRY_ADMIN_USER` / `FERRY_ADMIN_PASSWORD` in `.env` (the username defaults to `admin`). The schema needs no manual step — `storage.Open` runs `AutoMigrate` on every boot and it is idempotent, so re-running `start.sh` against an existing volume is safe. To move a database in from elsewhere, follow [deploy/MIGRATION-RUNBOOK.md](deploy/MIGRATION-RUNBOOK.md).

FAQ:

- **Port already in use** — `up` fails with a bind error. Change `FERRY_HTTP_PORT` or `FERRY_PANEL_PORT` in `.env` and rerun `start.sh`; the script prints the ports that were actually mapped, so there is no guessing.
- **Server does not become healthy / no database created** — `start.sh` prints the last 100 log lines and exits non-zero. The create and restore paths are documented in [deploy/MIGRATION-RUNBOOK.md](deploy/MIGRATION-RUNBOOK.md); stop with `docker compose --env-file .env -f deploy/docker-compose.yml down` before changing storage settings.
- **Credentials not configured** — `.env` ships with the security-relevant values commented out. An empty `FERRY_SECRET_KEY` means cloud API credentials are stored without encryption; empty `FERRY_ADMIN_SECRET` / `FERRY_API_SECRET` fall back to development constants, which is acceptable on a laptop and unsafe on a public network. Generate both with `openssl rand -hex 32`.
- **Container names collide with another checkout** — Compose derives the project name from the directory that holds the compose file, so the container prefix here is `deploy-` (`deploy-ferry-server-1`, …). Any other checkout, or any other repo that also keeps its compose file in a `deploy/` directory, on the same Docker daemon uses the same project name. Pass `-p <name>` to every command to disambiguate: `docker compose -p ferry-prod --env-file .env -f deploy/docker-compose.yml up -d`. Do not use `--remove-orphans` on a shared daemon — it deletes containers from unrelated projects that Compose mistakes for orphans.

**What runs in Compose:** `ferry-server` (Go API, SQLite by default), `ferry-dash` (admin dashboard), `ferry-panel` (user portal), and `ferry-postgres` (only with `--profile postgres`).

**Deliberately not in Compose:** `payments/` is a Go library compiled into `ferry-server` — it has no `main` and nothing to run as a container. `agent/` is a binary installed on each proxy node by [`deploy/agent-install.sh`](deploy/agent-install.sh): it dials out to the panel and runs as a systemd service on the customer machine, so it belongs to the nodes, not to the panel deployment.

## Repository Layout

```
ferry/
├── server/     # Go backend (gin + pure-Go SQLite, no CGO)
├── agent/      # Node agent (static Go binary, resident on proxy machines, outbound-only to the server)
├── panel/      # User portal (Vue 3: self-service subscription links, traffic lookup, usage instructions)
├── dash/       # Admin dashboard (Vue 3: users/nodes/configuration/statistics)
├── payments/   # Payment channel plugins (epusdt USDT integrated; WeChat/Alipay are placeholders pending merchant credentials)
├── packages/   # Shared contracts (agentproto, costref, payment provider interfaces)
├── deploy/     # Deployment files (compose, Caddy, one-click start.sh, migration runbook)
├── docs/       # Design documents
├── todo.md     # Feature breakdown and scheduling
└── Makefile    # Unified entry point for build / test / dev
```

## Compliance Positioning and Responsibility Boundary

ferry is a **proxy cluster management tool**; compliance responsibility for deployment and operation rests with the deployer. The tool does not provide proxy services itself, does not operate network access, and ships with no default destinations. Deployers are responsible for compliance with the laws and regulations of their jurisdiction and with their service providers' terms of service. See [docs/design/合规定位声明.md](docs/design/合规定位声明.md) (compliance positioning statement, in Chinese).

Proxy engines (Xray-core MPL-2.0, sing-box GPL-3.0-or-later, hysteria2 AGPL-3.0) are **binary-adapted only**: the agent manages them as external processes (start/stop/reload, config deploy) — no source import, no linking, no copyleft propagation to ferry. Operators install the engine binaries themselves. Full inventory: [THIRD_PARTY.md](THIRD_PARTY.md).

A survey of comparable panels (3x-ui / Marzban / Hiddify / Remnawave) is available at [docs/面板横向对比.md](docs/面板横向对比.md) (in Chinese).

## Development

```bash
make build   # Full build (server/agent/quic/dash/panel)
make test    # Go tests (server/agent/packages/payments) + frontend build checks
make dev     # Local development (dev-server / dev-dash / dev-panel can run individually)
make up      # One-click startup in containers (alias for ./deploy/start.sh)
make fmt     # gofmt
```

Frontend builds use Vite (both dash and panel). Node 24 is what dev machines currently run, not a hard requirement — `package.json` declares no `engines` field and the repo has no `.nvmrc`, so any Node version Vite supports works. The full set of environment variables is documented in `server/internal/config/config.go`; the HTTP API is defined by the route registrations in `server/internal/handler/router.go`. Implementation details for each capability are recorded in the corresponding design documents under [docs/design/](docs/design/) (see the "落地口径" implementation-notes section at the end of each document, in Chinese).
