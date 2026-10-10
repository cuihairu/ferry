# Third-Party Components

License inventory for ferry. Basis components are linked at source level
(Go imports / npm packages); proxy engines are **binary-adapted only**
(separate processes managed by the agent, no source import, no linking).

## Proxy engines (binary adaptation, process isolation)

ferry never imports or links engine source. The agent manages engine
binaries as external processes (`procs[]` in agent.json): start / stop /
reload / crash-restart, config deploy with rollback. No copyleft applies
to ferry from running unmodified engine binaries; see
[docs/design/开源选型设计.md](docs/design/开源选型设计.md) for the decision record.

| Engine | Upstream | License | Integration |
| --- | --- | --- | --- |
| Xray-core | https://github.com/XTLS/Xray-core | MPL-2.0 | binary only (`procs[]`), stats via its gRPC API |
| sing-box | https://github.com/SagerNet/sing-box | GPL-3.0-or-later | binary only (`procs[]`), config deploy, no source import |
| hysteria2 | https://github.com/apernet/hysteria | AGPL-3.0 | binary only (`procs[]`), config deploy, no source import |

Operators install the engine binaries themselves; ferry ships none of them
in its images or release archives.

## Basis components (source-linked)

Go modules (licenses verified at adoption; full machine-readable lists in
`server/go.mod`, `agent/go.mod`, `payments/go.mod`, `packages/*/go.mod`):

| Component | Use | License |
| --- | --- | --- |
| gin-gonic/gin | HTTP router | MIT |
| gorm + glebarez/sqlite + postgres/mysql drivers | storage | MIT / BSD / MIT |
| gorilla/websocket | agent server↔agent link | BSD-2-Clause |
| golang-jwt/jwt/v5 | admin auth | MIT |
| pquerna/otp | TOTP two-step | Apache-2.0 |
| robfig/cron/v3 | scheduled jobs | MIT |
| golang.org/x/crypto | argon2/ssh | BSD-3-Clause |
| gopkg.in/yaml.v3 | clash / hy2 kernel config | MIT |
| quic-go | QUIC transport (ferry-quic sidecar binary) | MIT |
| shirou/gopsutil | host metrics in agent | MIT |
| Vue 3 + Vite + Element Plus (dash/panel) | web frontends | MIT |

Frontend lockfiles (`dash/package-lock.json` style via pnpm) carry the full
transitive set; all basis-component licenses are permissive (MIT / BSD /
Apache-2.0).
