[English](herald-integration-log.en.md) | [中文](herald-对接实录.md)

# herald Integration Log (HERALD-1~5)

> This log was handed over on 2026-10-08 from the herald repo's `docs/guide/ferry-integration.md` into this repo for archiving: integration-party private details (outbox tables, `FERRY_*` config, shim, HERALD-x change items) are recorded by the integration party's own repo, and the herald repo docs keep only the generic integration guide (its `docs/guide/integration.md`). §N.N in this document refers to section numbers in the herald repo's `docs/design-audience-relations.md` (the herald design doc); herald is the dependency of this system's delivery infrastructure, so it is cited truthfully as an upstream source.
>
> The last link of the three-stage "audience layer improvement": ferry (event source, HERALD-1/2/3/4 already landed) and herald (delivery infrastructure, its batches 1–11 already landed) integrated as real processes, verifying the **receive event → distribute → leave receipt trace** closed loop. This document is the verification record and discrepancy list from the 2026-10-08 joint debugging session, and also serves as the integration configuration draft for later deployment.

## 1. Environment topology

Three processes on one machine (local joint debugging, no production involved):

```text
ferry :18080 ──① POST /api/v1/apps/ferry/events──→ heraldd :18081
   ↑                                                      │
   │ ③ POST /api/internal/event-results                    ② webhook channel delivery
   │        (now handled natively by this repo; during joint debugging a shim translated §13.5 → ferry vocabulary)  ↓
shim :18090  ←──§13.5 callback (HMAC signature verification)──────────────────────┘
   └── /hook: webhook delivery drop point (evidence archived)
```

- **ferry**: clean checkout of origin/main (fb851d3), sqlite, `FERRY_HERALD_URL=http://127.0.0.1:18081/api/v1/apps/ferry`, `FERRY_HERALD_TOKEN=<trigger token>`, `FERRY_EVENT_FLUSH_SEC=2`;
- **heraldd**: memory queue + webhook provider (pointing at the shim's `/hook`) + `apps.ferry` seeded with three tokens + `sources.enabled: true` (the contact-surface binding surface hangs under this switch);
- **shim**: a one-page Go process (~90 lines) used during joint debugging, two shims — §13.5 callback receiving (HMAC signature verification + vocabulary translation) and the webhook delivery drop point; a runnable sample of the HERALD-5 change. **HERALD-5 has now landed natively** (`server/internal/handler/eventsapi.go`: `verifyHeraldSignature` verifies `X-Herald-Signature`, natively parses the §13.5 payload, parses the numeric `delivery.event_id` back to the outbox id, maps `success→sent`), the shim retired accordingly, and this diagram is kept as evidence of the topology on the day of joint debugging.

## 2. Event vocabulary mapping (告警通道设计 §3 → herald §13.3 dispatch)

ferry's `POST {FERRY_HERALD_URL}/events` payload maps onto herald's namespace trigger surface:

| §3 field | §13.3 destination | Notes |
| --- | --- | --- |
| `id` | `event_id` | ferry outbox primary key, §11.1 idempotency + §13.5 callback **carried back** — the key that associates a receipt with its original event |
| `kind` | `category` | must already be registered in the namespace (unregistered gives 422, no silent fallback) |
| `severity` | `urgency` | critical→critical, warning→urgent, info→normal; anything else gives 422 |
| `target` | `audiences[1]` | ref form (`user:<id>`) maps to audience id `user.<id>` — `:` is reserved for herald expandRef references, and the id vocabulary does not include it; non-ref form (`admin`) passes through as-is |
| `dedup_key` | `dedup_key` | dedupe gate fallback |
| `meta` | `params` | arbitrary JSON object, template parameters |
| `title`/`body` | direct delivery content | no template involved |
| `occurred_at` | ——（no counterpart surface） | herald uses its own timestamps for auditing/callbacks; informational field |

Deployment-side convention: a ferry user `user:N` has audience id `user.N` in herald, and surface binding (herald's `POST /api/v1/audiences/{id}/surfaces`) uses the same id.

## 3. Closed-loop measured evidence (2026-10-08)

Trigger point: admin-side announcement fanout on the ferry side (`POST /api/notifications/announcement`, the HERALD-4 wiring point, one notice event per enabled user). Two in a row confirm repeatability:

| Leg | Evidence |
| --- | --- |
| ① ferry→herald | outbox `events` rows id=1/2 both `sent` (attempts=1); `event_deliveries` holds `1\|herald\|sent`, `2\|herald\|sent` |
| ② herald distribution | herald `GET /api/v1/apps/ferry/deliveries`: `source: app:ferry`, `audience_id: user.1`, `category: notice`, `provider: webhook`, `status: success`; the shim's `/hook` received the rendered title and body |
| ③ receipt trace | shim verified the signature (HMAC passed) → translated → ferry `HTTP 200`; `event_deliveries` holds `1\|webhook\|sent`, `2\|webhook\|sent` — **the receipts matched the original events by outbox id** |

herald's §13.4 audit stream being empty for these two events is correct behavior: the audit answers "relationship change + dedupe folding"; these two announcements had no dedup_key and different fingerprints, so there was no folding and no relationship change.

## 4. Discrepancy list (recorded truthfully, everything fixable already fixed)

1. **No machine entry point for surface binding (blocking, fixed)** — bot/official account only cover platform-endorsed follows, and the preference center can only switch categories on existing contact surfaces; an integrator like ferry where "user contact info lives in its own database" has no API at all to land bindings. Fix (herald side): add `POST/DELETE /api/v1/audiences/{id}/surfaces` (binding on behalf of the user, going through herald's `SourceAdapter.Follow/Unfollow`, with the audit recording the entry as `admin`).
2. **Callbacks do not carry back the integrator's event identity (blocking, fixed)** — §13.5 `delivery_result` has only herald's own task_id, so an integrator cannot map it back to its own event. Fix (herald side): `DeliveryTask.EventID` runs through delivery (the planner carries it in from the notification), and the callback payload carries `delivery.event_id` back; the ferry outbox id enters through the `/events` adapter surface and comes home through the callback.
3. **ferry's receipt endpoint does not match the §13.5 vocabulary (ferry side, landed natively)** — the original `POST /api/internal/event-results` bound `{event_id: int64, channel, status: sent|failed, detail}` (HERALD-2 landed before herald batch 11, designed against a guessed shape); herald's §13.5 event is `{event_id: "cb-…", app, kind, at, delivery: {…}}`, so connecting directly gives 400. **HERALD-5 has landed**: the receipt endpoint natively parses §13.5 — verifies the `X-Herald-Signature` HMAC, takes `delivery.event_id` (int parse), `delivery.channel`→channel, `success→sent` (`server/internal/handler/eventsapi.go`, including signature verification and measured 400 semantics); the joint-debugging shim was the runnable sample of that change and is now retired.
4. **target ref collides with the audience id vocabulary (fixed on herald side, mapping convention)** — the `:` in `user:<id>` collides with herald expandRef references and fails audience id validation; the adapter surface maps it to `user.<id>` (the §2 table), and the document itself is the convention.
5. **`occurred_at` has no counterpart surface (recorded, not blocking)** — §13.3 has no event-occurrence-time field; if herald's audit later needs to answer "when did the event happen", one column can be added along with its audit detail batch.

## 5. Conclusion on converging the webhook channel

**Convergence is possible**, conditions are in place:

| | ferry `notify.Webhook` (P1-10, current) | herald webhook channel (after integration) |
| --- | --- | --- |
| Delivery | one global URL, fire-and-forget, 5s timeout | equivalent POST capability + the full pipeline: retry budget, at-least-once |
| Failure | failures only logged, **silently dropped** | receipts leave traces (ferry dash shows red), dead letters can be re-sent by hand |
| Routing | none (all events lumped together) | per user, by the three-way intersection of audience relationship × strength × contact surface |
| Authentication | static header `X-Ferry-Webhook-Secret` | per-app HMAC-SHA256 callback signature (verifiable origin, tamper-proof) |
| Content | `{event, text, fields}` one-line summary | template rendering / direct delivery content, multiple formats |

Suggested convergence order: ① on the receiving side, swap the `{event,text,fields}` adapter for the herald webhook payload (`{id, provider, level, targets, timestamp, title, body}`, plus an optional `raw` original-field fallback key) — a one-off change; ② after ferry has `FERRY_HERALD_URL/TOKEN` configured, run `notify_webhook_url` settings and event egress **side by side for one alert cycle** to observe; ③ remove the settings entry and retire the `internal/notify` package. The only production point that still carries webhook direct-send semantics is recovery's BR-5 (`recovery_failed` escalation to a human); since HERALD-3 it runs dual-track with the outbox — once the parallel period ends, a single track suffices.

## 6. Deployment draft (minimum config for production integration)

```yaml
# heraldd side: seed the ferry namespace
apps:
  ferry:
    tokens:
      - secret: "$FERRY_HERALD_TOKEN"   # trigger scope, used by the ferry delivery leg
      - secret: "$FERRY_HERALD_CONFIG_TOKEN"   # config scope, for categories/callbacks/bindings
      - secret: "$FERRY_HERALD_QUERY_TOKEN"    # query scope, for reconciliation reads
sources:
  enabled: true    # the surface binding surface hangs under this switch
```

```bash
# ferry side: environment variables
FERRY_HERALD_URL=https://herald.example.com/api/v1/apps/ferry
FERRY_HERALD_TOKEN=$FERRY_HERALD_TOKEN
```

Initialization (run once with the config token): register categories (one for alerts, one for outreach, registered in full per the lists in 告警通道设计 §2.1/§2.2 — currently 13 alert kinds + 7 outreach kinds) → herald `PUT /api/v1/apps/ferry/callback` (pointing at ferry's receipt endpoint + a 32-byte secret) → for each user, bind a contact surface with herald's `POST /api/v1/audiences/user.N/surfaces`. Exposed surface: `/api/internal/event-results` keeps ferry's current convention (unauthenticated `/api` group, isolated by network boundary on the deployment side); after the native signature verification of HERALD-5 landed it also has application-layer anti-forgery.