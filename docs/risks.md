# Scope, risks and what is not built

Honest status at submission (2026-10-01). Everything below is something the original plan (CLAUDE.md) called for
that this submission does **not** deliver. Nothing here is claimed anywhere else in the docs.

## Not built

| Area | Plan | Status |
|---|---|---|
| fleet-api (public REST/WebSocket, Keycloak OIDC + JWT, RBAC, rate limit, audit log, erasure) | F-10, F-11 | **Not built.** The two internal APIs take dev identity headers (`X-Tenant-Id`, `X-User-Id`, `X-Roles`); tenant isolation is enforced by Postgres RLS (tested) and 404-not-403. Compose binds them to `127.0.0.1`. **Must not be exposed beyond localhost.** |
| Web console | React + MapLibre, 7 pages | **One static HTML page** (`web/index.html`): battery health, alerts, vehicle SoH + DTC decode, depot plan + approve. No live map. |
| Batch analytics on Iceberg, backfill | F-12 | Not built. |
| Dashboards (Grafana/Loki/Tempo, OTel) | F-13 | Not built. Services expose Prometheus `/metrics`. |
| DTC runbook RAG, fault-signature similarity (pgvector) | F-06 (RAG part), F-17 | Not built. DTC decode against the catalogue works. |
| En-route charger dispatch (A* + assignment) | F-09 | Not built. |
| Fleet copilot | F-18 | Not built. |
| Carbon report | F-19 | Not built. |
| Helm, Terraform, second cloud, Strimzi | DevOps pack | Not built. Docker Compose only. |
| 100K events/s cluster load test, chaos tests (kill broker/pod) | NFR evidence | Not run. Local runs reached about 12 K events/s on one laptop (`docs/evidence/load/`). |
| Device mTLS / TLS 1.3 / vault | Security | Not built. Gateway HTTPS batch uses HMAC; MQTT is anonymous in dev. |
| Model serving | F-14/F-15 | Models are trained and evaluated (`ml/`); battery-intel does not serve them yet. |

## Known limitations of what is built

- **Depot back-test saving is below target:** energy cost -10.75 %, energy + degradation -5.78 % vs the 15 % target
  (`docs/evidence/dispatch/2026-10-01-backtest-5x30/`). The site cap never binds on the seed depots; the connector count does.
  The tariff is synthetic and the degradation coefficients are assumptions (`config/degradation.toml`).
- **SoH from the pipeline** is estimated from the BMS SoC and current; its accuracy against truth is only measured offline
  in `ml/` (leakage guard), not by the running service.
- **Latency NFRs** (alert p95 < 5 s, API p95 < 200 ms) were not demonstrated at scale; local numbers carry laptop caveats.
- **Simulated data only.** Tariffs, degradation, DTC descriptions (`source_status = unverified`) are synthetic or unverified.
- Single Kafka broker (RF 1) and single instance of each service locally.

## Risks we hit and how they were handled

| Risk | What happened | Handling |
|---|---|---|
| Duplicate suppression losing data | Recording a dedup key before the Kafka ack dropped the retry | Remember only after ack + in-flight set (ADR-0004, tests) |
| Slow sink stalling alerts | Scylla writes coupled to alert latency (21-70 s) | Two roles / consumer groups; alerts 20 ms gateway-to-Kafka |
| Worker hang on coordinator loss | Synchronous commit blocked forever under laptop load | Offsets stored per record, committed in the background |
| Relay blocked by RLS | `UPDATE` re-checks the SELECT policy | Policy fixed; duplicate publish tolerated by `plan_id` dedupe |
| Laptop memory | Docker could take 14.5 GB | WSL capped at 5 GB, per-container limits in compose |

ER diagram: `docs/er/README.md` (as built). Not captured: EXPLAIN ANALYZE before/after for the top three queries, `docs/capacity.md` measurements.
