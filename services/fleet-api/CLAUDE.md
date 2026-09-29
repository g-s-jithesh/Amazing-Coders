# fleet-api — CLAUDE.md

Python 3.12 / FastAPI. The **public API and system of record**: tenants, fleets, vehicles, alerts, audit, and the approval workflow. It is the CQRS read side for the UI, the alert persistence consumer, and the outbox relay. NFR: **API p95 < 200 ms, p99 < 500 ms** (root §2, §9, §11).

## Owns

- Postgres schema `fleet`: tenant, app_user, role, user_role, subscription, fleet, depot, vehicle_model, battery_pack, vehicle, vehicle_duty, driver, charger_site, charger, alert, alert_ack, audit_log, erasure_request, idempotency_key, outbox.
- Workers (same image, different entrypoint):
  - `alert_consumer`: `alerts.v1` → `alert` table + WebSocket push
  - `outbox_relay`: `outbox` → Kafka
  - `audit_consumer`: `audit.v1` reads → `audit_log`
  - `mv_refresher`

It does **not** own SoH, models or DTC knowledge (battery-intel), or plans and tariffs (dispatch-optimizer). Call those over HTTP. Never read their schemas.

## Layout

```
app/
  api/v1/routers/        # vehicles.py, fleets.py, alerts.py, dispatch.py, reports.py, privacy.py, audit.py, ws.py
  api/v1/dto/            # Pydantic request/response models (explicit fields only; no ORM passthrough)
  api/deps.py            # auth, tenant session, permissions, rate limit, idempotency
  application/           # use cases: GetVehicleOverview, ListAtRiskVehicles, AckAlert, ApproveDispatchPlan, RequestErasure …
  domain/                # entities, permissions matrix, masking policy, cursor codec (pure)
  infrastructure/
    db/                  # SQLAlchemy 2 async engine, models, repositories
    clients/             # battery_intel.py, dispatch.py (httpx + retry + circuit breaker)
    kafka/ redis/ vault/ keycloak/
  workers/               # alert_consumer.py, outbox_relay.py, audit_consumer.py, mv_refresher.py
  main.py                # composition root
migrations/              # Alembic (includes RLS policies + roles)
tests/{unit,integration,contract}
```

## Tenancy and RLS (non-negotiable)

- Two DB roles: `kilowatt_owner` (migrations only) and `kilowatt_app` (runtime; **no BYPASSRLS**, no UPDATE or DELETE on `audit_log`).
- Every tenant table has `tenant_id` + an RLS policy `USING (tenant_id = current_setting('app.tenant_id')::uuid)`.
- The `get_tenant_session` dependency opens a transaction and runs `SET LOCAL app.tenant_id = :tid` from the **verified JWT**. There is no other way to get a session in routers.
- Cross-tenant access returns **404** (the RLS filter makes the row invisible). Test this on every `{vin}`/`{id}` route.

## AuthN/Z

- Keycloak OIDC. Verify the JWT signature (JWKS cached, with rotation), `aud`, `iss`, `exp`. `tenant_id` and roles come from claims.
- The permission matrix lives in `domain/permissions.py` (e.g. `vehicle:read`, `location:precise`, `alert:ack`, `dispatch:approve`, `audit:read`, `privacy:erase`). Routers check permissions, never role names.
- **Location masking** happens in the DTO mapping: without `location:precise`, return `geohash5` and drop lat/lon (also over WebSocket).

## API conventions

- **Keyset pagination:** cursor = base64url(JSON `{k: [created_at, id]}`) + HMAC, so a tampered cursor → 400. Never OFFSET.
- **Errors:** RFC 9457 `application/problem+json` through one exception handler. No stack traces in responses.
- **Rate limiting:** a Redis token bucket via a Lua script, keyed by `tenant:user:route-class`. Returns 429 + `Retry-After` + `RateLimit-*` headers.
- **Idempotency-Key** on POST approve/plan/erasure: store `(tenant, key) → status + body hash` for 24 h, and replay the stored response.
- **Live updates:** `/ws/v1/fleet/{fleet_id}` subscribes to Redis `fleet:{id}:deltas` + alert pushes. The client auth token is checked on connect and on expiry. Per-connection send queue with drop-oldest for position deltas (never for alerts).

## Outbox and approval

`POST /dispatch-plans/{id}/approve`:
1. Check the permission.
2. In **one transaction**, call dispatch-optimizer to transition the plan to APPROVED, write an `outbox` row (`dispatch.commands.v1`) and an `audit_log` row.
3. The relay publishes with the plan version as the idempotency key, and marks the row sent.

If dispatch-optimizer is down → 503 with a problem body, from the circuit breaker. Never approve half a plan.

## Audit

- Every read of vehicle or driver data and every mutation is audited: actor, tenant, action, resource, purpose, trace_id, result.
- Mutations: audit row in the same transaction. Reads: an async event on `audit.v1` (batched), persisted by `audit_consumer`. A test proves read-audit completeness under load.

## Erasure (crypto-shredding)

`POST /privacy/erasure-requests` (`privacy:erase`) → a job that:
1. deletes the per-driver key in the vault transit engine
2. deletes the driver row
3. tombstones the compacted topics
4. records completion

Verify in `tests/compliance/`: PII is unrecoverable and `driver_ref` no longer resolves.

## Performance

- Hot reads come from Redis (vehicle state) or the materialised views. Postgres only for lists with indexes proven in `docs/sql-optimisation/`.
- Avoid N+1: `selectinload`/`joinedload`, and tests assert the query count (a SQLAlchemy event listener counts statements).
- One async engine per process. Pool sized by config. Timeouts on every outbound call (connect 0.5 s, read 2 s).

## Test focus

- Cross-tenant 404 on every route (parametrised).
- Permission matrix.
- Location masking (REST + WS).
- Cursor tamper → 400.
- 429 behaviour.
- Idempotent replay.
- Outbox atomicity (kill the process between commit and publish → the relay still publishes exactly once per plan version).
- Audit completeness.
- Erasure.
- Query-count tests.
- Pact provider verification.
- Schemathesis fuzzing against the OpenAPI spec.
