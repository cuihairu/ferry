[English](panel-comparison.en.md) | [中文](面板横向对比.md)

# Panel Landscape Comparison (research appendix)

> Wrap-up of the "research sources" tail at the top of todo: add the Remnawave research and land the landscape comparison. Conclusions are for ferry to reference when weighing trade-offs only; they do not create new work items (P2 stays unstarted per the sign-off).

## 0. Research subject and how material was taken

- Remnawave: entry repo `remnawave/panel` (GitHub) — now the docs site and release entry point (docusaurus source lives in the repo under `docs/`, no Go/TS code); the implementation is in `remnawave/backend` (panel backend), `remnawave/frontend` (admin React app), `remnawave/node` (node), `remnawave/xtls-sdk` (TS SDK for Xray-core). Source: `docs/overview/components.md`.
- Material: shallow clone of `remnawave/backend` (2026-10-08 snapshot) read file by file; doc citations point at the mdx source files under `docs/` in the `panel` repo.
- **Correction of a widespread claim**: the Remnawave backend is not Go — `package.json` depends on `@nestjs/core 11.2.1` and `@prisma/client 6.19.3`, and `src/` is entirely TypeScript (NestJS + Prisma + PostgreSQL); the node (`remnawave/node`) is TypeScript too, with Xray-core embedded in the container (via `@remnawave/xtls-sdk` wrapping the Xray API — see the SDK description in `components.md`). "Go panel" is a myth.

## 1. Remnawave mechanism-by-mechanism research (file-level sources)

### 1.1 Node and squad model (three tiers: node / host / squad)

- **Nodes** carry no inbound config of their own: they reference a config profile via `activeConfigProfileUuid`. The `Nodes` model in `prisma/schema.prisma` (L164-220): `isConnected/isConnecting/isDisabled` connection state machine, `consumptionMultiplier/nodeConsumptionMultiplier` consumption multiplier (default 1e9 = 1.0x), `countryCode/tags/ips`, `providerUuid` linking to providers (`InfraProviders`/`InfraBilling*` machine billing and budget).
- **ConfigProfiles** store Xray config templates; `ConfigProfileInbounds` are the inbounds inside a profile, and `ConfigProfileInboundsToNodes` assigns inbounds to nodes — which inbound is selected is done in the Web UI (the "Selecting active inbounds on nodes" row of the official comparison table; Marzban instead uses node environment variables).
- **Hosts** are the user-facing connection info layer (addresses/disguise shared out): the `Hosts` model (schema L267-315) holds `address/port/path/sni/host/alpn/fingerprint/securityLayer`, `xhttpExtraParams/muxParams/sockoptParams`, `excludeFromSubscriptionTypes` (exclusion per subscription format), `internalSquadsMode` (EXCLUDE/ALLOW_ONLY); they bind to nodes via `HostsToNodes`.
- **Squads** are the bulk distribution unit: `InternalSquads` (internal squads — `InternalSquadHostLinks` binds hosts, `InternalSquadInbounds` binds inbounds, `InternalSquadMembers` takes users, and a user hangs on a single `externalSquadUuid`) and `ExternalSquads` (external squads — subscription lists shared across panels, `ExternalSquadsTemplates`).
- Node traffic also has a budget surface: `Nodes.trafficLimitBytes/trafficUsedBytes/trafficResetDay/notifyPercent` (schema L188-191) — node-level limits, reset day, and alert percentage.

### 1.2 Subscription surface (sub formats)

- Six response formats: `RESPONSE_RULES_RESPONSE_TYPES` = `MIHOMO / CLASH / STASH / SINGBOX / XRAY_BASE64 / XRAY_JSON` (`src/modules/subscription-template/constants/config-types.ts`); generators map one to one (`src/modules/subscription-template/generators/{clash,mihomo,singbox,xray-json,xray}.generator.service.ts`).
- Multiple templates are possible (`SubscriptionTemplate` + `render-templates.service.ts`); there are also response rules (`subscription-response-rules`, routing by UA/device to different formats) and global subscription settings (`subscription-settings`).
- The subscription page is its own module: `SubscriptionPageConfig` (self-hosted subscription landing page; the official comparison table's "Multiple subscription pages" row says multiple pages can be installed).
- Subscription identity uses a short ID: `Users.shortUuid` (schema L48).

### 1.3 Traffic accounting and quotas

- Real-time user usage is a **single-row table**: `UserTraffic` (schema L86-102) `usedTrafficBytes/lifetimeUsedTrafficBytes/onlineAt/lastConnectedNodeUuid/firstConnectedAt` — online state and last connected node land in the same row; it is not per-record logging (contrast ferry's `traffic_logs` per-record accounting).
- Two history aggregate tables: `NodesUserUsageHistory` (user × node) and `NodesUsageHistory` (node level, schema L234/L251).
- User quota: `trafficLimitBytes` + `trafficLimitStrategy` (`NO_RESET/DAY/WEEK/MONTH/MONTH_ROLLING`, schema L54) — the **MONTH_ROLLING** window (rolling one month from the last reset time) is a tier ferry's `reset_cycle` (none/day/week/month) does not have; `lastTrafficResetAt` records the last reset.
- Four user states: `ACTIVE/DISABLED/LIMITED/EXPIRED` (schema L50-51) — over quota (LIMITED) and expired (EXPIRED) are expressed separately; the official comparison table confirms there is no Marzban-style "On Hold" state that pauses accrual.
- Credentials are pre-generated on the user row: `trojanPassword/vlessUuid/ssPassword` (schema L57-59); the host layer assembles the share link.

### 1.4 User outreach and alerting

- Two channels: Telegram + Webhook, **switched per event per channel** — the config file `configs/notifications/notifications-config.yml` is mounted into the container (`docs/features/fine-tuned-notifications.md`, since 2.3.1), shaped like `events: user.created: {telegram: true, webhook: false}`.
- Event coverage: user create/delete/update, `user.bandwidth_usage_threshold_reached` (usage thresholds such as 80%/90%), node up/down, node traffic thresholds, `user_hwid_devices.added` (new device bound), `errors.bandwidth_usage_threshold_reached_max_notifications` (self-throttling once the notification cap is reached to prevent spam; same file L108-109/L174/L217-218).
- Duplicate push suppression relies on a remembered field: `Users.lastTriggeredThreshold` records the tier already fired, reset to zero when the quota changes (`src/modules/users/users.service.ts` L167).
- Webhook is signed: `X-Remnawave-Signature` (signature via `WEBHOOK_SECRET_HEADER`) + `X-Remnawave-Timestamp` (`docs/features/receiving-webooks.md`; the URL supports several comma-separated entries).
- The built-in TG bot only does notifications and TG OAuth login (admin side, `docs/features/telegram-oauth.md`); it offers no user self-service query commands.
- Others: HWID device count limits (`Users.hwidDeviceLimit` + `HwidUserDevices`), torrent-blocker interception reports (`TorrentBlockerReports` table), rescue-cli admin self-recovery (`docs/features/rescue-cli.md`).

### 1.5 Deployment shape

- Minimum deployment is three containers: `remnawave/backend:3` + `postgres:18.4` + `valkey/valkey:9-alpine` (`docker-compose-prod.yml`) — PostgreSQL and Valkey are hard dependencies; there is no SQLite tier.
- The advanced orchestration splits backend into three services: `remnawave-rest-api / remnawave-scheduler / remnawave-processor` (`docker-compose-advanced-prod.yml`) — API, scheduled jobs, and queue consumption in separate processes.
- Official system requirements: panel 2GB RAM / 2 cores / 20GB disk (the System requirements row of the official table in `docs/overview/comparison-of-functions.md`; the same table notes Marzban's 1GB as a community figure).
- Other rows of the official comparison table (same file, self-described as "from former Marzban users' experience, not a uniquely reliable source") worth referencing: config stored in DB (contrast Marzban's file-based config), config validation includes full XRAY validation, keeps working when nodes disconnect, no VMESS host surface (needs core-level config to add), no multi-admin, HWID device limit unique (2026-10-11 note: ferry P2-4 has landed the same surface — HWID device count and source IP count limits on the subscriber side, so "unique" no longer holds).

## 2. Landscape comparison (six dimensions used inside the repo)

> The 3x-ui / Marzban / Hiddify columns only record facts already cited inside the repo (`来源` citations in the todo items; repos were not re-pulled for re-verification); the Remnawave column is this batch's material; the ferry column is the current state.

| Dimension | 3x-ui | Marzban | Hiddify | Remnawave | ferry current state |
| --- | --- | --- | --- | --- | --- |
| Language/shape | Go, xray embedded in one process | Python | Django | **TS/NestJS** + React + TS node | Go (single-binary panel backend + separate agent/quic binaries) |
| Protocol coverage | inbound model, configured per inbound (`internal/database/model/model.go`) | panel-level inbounds (`/api/inbounds`) | panel-level | Xray family mainly (VLESS/XTLS/REALITY/Trojan/SS), **VMESS host surface missing** (self-admitted in the official table; core level can add it) | vless/vmess/trojan/ss + hysteria2 (hy2 batch, binary adapted), five protocols with per-user credentials (`sub.Entry`) |
| Node and squad model | single machine, no node orchestration | node registration + inbound push | multi-node | three tiers of node/host/squad: ConfigProfiles for profiles + Hosts for the connection layer + Internal/ExternalSquads for bulk distribution; node-level traffic budget and consumption multiplier | node (entry/landing/both) + pool state machine (active/suspend/standby) + landing squads (landings/pool); entry domains entry_domains |
| Subscription surface | base64 links + clash YAML (`internal/sub/*`) | `/sub/{token}` routed by UA + clash-meta | api_key is the subscription identity | six formats (mihomo/clash/stash/singbox/xray-base64/xray-json) + templates/response rules/standalone subscription pages, multiple pages allowed | `/sub/:token` with three formats (v2ray/clash/singbox; the SB batch added sing-box outbound for all five protocols) + UA adaptation + `subscription-userinfo` header + disconnect-resilience notes (TOUCH-7) |
| Traffic accounting and quotas | per-inbound statistics | `record_usages` batch accounting + `review_users` table sweep | `DailyUsage` daily aggregation + `is_active` computation | **UserTraffic single-row real time** (incl. online state/last node) + two history aggregate tables; four user states; MONTH_ROLLING rolling reset | `traffic_logs` per-record accounting + reset-cycle windowed queries + subscription lock on expiry/over-quota (P0-8/P0-11) + quota-linked plan downgrade (SAVE-6) |
| User outreach and alerting | TG bot notifications | TG bot notifications + admin | email surface | TG+Webhook dual channel with per-event switches, threshold tier memory against spam, signed webhook, HWID device limits | in-app notices + Herald distribution (TG/email via channel plugins), the full TOUCH-1..7 set (bind/rebind, stale closure, monthly bill, routine email, RSS, bot integration surface, disconnect resilience), subscriber-side device count/IP count limits (P2-4: the HWID surface depends on client reporting, the IP surface is always in effect; the panel shows the registered count and the cap) |
| Deployment shape | single binary | single container + optional extra DBs (SQLite included) | single container | **three containers minimum** (backend + PostgreSQL + Valkey, no SQLite tier; advanced splits three services); official recommendation 2GB RAM | single-binary panel backend + SQLite/PG/MySQL drivers, SQLite as the default small-memory tier (plus separate agent/quic binaries) |

## 3. Take-aways for ferry (single-machine, small-memory positioning)

Observations only, no new work pulled in:

1. **Shape verdict**: Remnawave's three-tier model (config profile / host / squad) + hard three-container dependencies + the official 2GB RAM recommendation is a shape aimed at scaled multi-machine operation; ferry's two-tier node/pool-state/entry-domain model closes the same orchestration needs inside one machine, and the default SQLite tier is consistent with the "small-memory VPS" positioning — **keep the status quo, do not move just to align**.
2. **Mechanisms worth watching** (ferry already has equivalents or limited gain):
   - the `MONTH_ROLLING` rolling reset tier: ferry's `reset_cycle` has no such tier and the natural-month bill (TOUCH-4) is enough for a small site, so just watch it;
   - `lastTriggeredThreshold` threshold tier memory: ferry's alert surface uses same-day dedupe plus outreach records (TOUCH-5) — mechanism equivalent;
   - outbound webhook signature header: ferry's event surface is outbox + Herald receipts (the opposite direction — ferry is the producer), different shape, no need to align;
   - node-level traffic budget + alert percentage (`Nodes.notifyPercent`): ferry has probing and a pool state machine on the node side but no traffic budget; revisit if multi-machine scale arrives later (watch only, do not start).
3. **Do-not-copy list**: a three-container minimum with hard PostgreSQL/Valkey dependencies conflicts with the single-machine small-memory positioning; subscription pages / response rules / the three squad tiers are over-engineering at the current scale; a TG bot that only notifies without self-service queries is already covered by ferry's TOUCH-6 read-only integration surface, which does not depend on the panel domain being alive.
4. **Correction archived for later reuse**: the community commonly treats Remnawave as a "Go panel" while it is actually TS/NestJS — future references to it as a "Go peer implementation" should be corrected.

---

> Compliance positioning: ferry is a proxy cluster management tool; deployment and operational compliance responsibility lies with the deployer (see the Compliance Positioning Statement). This appendix is a competitive research record and contains no runtime code references.