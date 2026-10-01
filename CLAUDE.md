# CLAUDE.md — Project Kilowatt

> **EV Fleet Battery Health & Intelligent Charging Dispatch (with Fault Diagnostics)**
> Motorq Connected Vehicle Intelligence Hackathon (issued by Talenciaglobal). Motorq is only an industry reference; this is not affiliated with Motorq.
> "Project Kilowatt" is a working codename. Rename it everywhere once the team settles on a name.

This file is the operating manual for anyone (human or AI) working in this repository. Read it fully before making changes. When this file and the code disagree, fix one of them in the same change.

---

## 0. Prime directives (read these even if you skip the rest)

1. **Depth beats breadth.** The brief is open about *what* we build and strict about *how well* we build it. One problem (EV battery health + charging dispatch + fault diagnostics), built to production quality, beats five shallow features.
2. **Every feature must be traceable** to code, tests, and a demo timestamp. Update `docs/feature-matrix.md` in the same PR that adds or changes a feature.
3. **Testing carries a lot of weight.** No feature is "Done" without unit tests, and without integration tests where it touches a broker, database, or cache. CI must stay green.
4. **Never fabricate numbers.** Every throughput, latency, accuracy, or cost figure in the docs must come from a reproducible run whose raw output is stored in `docs/evidence/`, with the hardware and cluster size recorded.
5. **Synthetic or public data only.** No real VINs, owner data, or personal data, ever. Simulated VINs use the synthetic WMIs listed in `data/reference/wmi_synthetic.csv`.
6. **No secrets in git.** Configuration goes in env vars (12-factor), secrets go in Vault/OpenBao, and `.env.example` is the only env file committed.
7. **Cloud-agnostic.** Services must not import cloud-vendor SDKs directly. Object storage goes through the S3 API behind a port, with the endpoint set by config (MinIO locally, S3/GCS/Azure-compatible in the cloud).
8. **Keep the domain layer pure.** No framework, ORM, or broker imports in `domain/`.
9. **Declare AI and open-source use.** Log what AI assistants (including Claude) were used for in `docs/declarations.md`. List OSS components and licences in the SBOM.

---

## 1. The problem we are solving

### 1.1 Problem statement (the template's format)

> **EV fleet operations managers** need a way to **know which vehicles' batteries are degrading or about to fault, and to charge every vehicle at the lowest total cost without missing a departure**, because **today charging is ad-hoc ("plug in on arrival"), battery faults are found only after a breakdown, and DTCs arrive as raw codes nobody can act on**. This **costs** peak-tariff energy bills, depot demand-charge penalties, accelerated battery ageing (the pack is the most expensive part of the vehicle), and unplanned downtime.

- **Primary user:** EV fleet operations manager / charging dispatcher.
- **Secondary stakeholders:** depot technicians (fault diagnostics), fleet finance (energy cost), drivers (range confidence), OEMs and lessors (residual value depends on SoH), sustainability teams (kWh → CO₂).

### 1.2 The three questions the product answers

| # | Question | Module |
|---|----------|--------|
| Q1 | *Which batteries are unhealthy or about to fault in the next 7 days, and why?* | `battery-intel` (SoH, RUL, fault-risk, DTC diagnostics) |
| Q2 | *When and where should each vehicle charge at the lowest cost, while staying within depot power limits and meeting every departure?* | `dispatch-optimizer` (depot DP + en-route A* + assignment) |
| Q3 | *Something just went wrong. What is it and what do I do now?* | `stream-processor` real-time alerts + `copilot-agent` (RAG over runbooks + similar past faults) |

### 1.3 Innovation pillars (Solution Doc §3.3, at most 3)

1. **Health-aware charging cost.** The optimiser's objective is `energy cost + battery degradation cost + lateness penalty`, not just tariff cost. Degradation cost is estimated from the ageing model (high SoC dwell, high C-rate, hot pack) and priced in ₹ per % SoH lost.
2. **Fault-signature similarity search.** The pre-fault telemetry window is embedded as a fixed-length vector in pgvector, so we can answer "these 12 vehicles look like the ones that threw P0A7E within 5 days".
3. **Guarded fleet copilot with privacy by construction.** The agent can only *propose* dispatch changes (a human approves), every tool call is audit-logged, and driver data is erasable through crypto-shredding, even from immutable Kafka and Parquet history.

---

## 2. How we score (map every piece of work to the brief)

Minimum bar (non-negotiable):

- [ ] Our own simulator produces **≥100,000 vehicles**.
- [ ] **Real-time** processing **and** batch analytics over historical data.
- [ ] **Relational + NoSQL + vector** storage, each one justified.
- [ ] **Secure APIs + usable web UI**.
- [ ] **Containerised, tested, deployable** on at least one cloud with **no code change** for a second cloud.

NFR targets (Solution Doc §7):

| NFR | Target | Where proven |
|-----|--------|--------------|
| Throughput | ≥100K events/s; 3× burst for 5 min, no data loss | `tests/load/` + `docs/evidence/load/` |
| Latency | ingest→dashboard < 2 s; critical alert < 5 s; API p95 < 200 ms, p99 < 500 ms | OTel traces + k6 |
| Scalability | stateless services scale horizontally; adding brokers or nodes needs no code change | HPA + partition count |
| Availability | no single point of failure; 99.9% target; recovers after a broker or pod is killed | `tests/chaos/` |
| Security | OIDC + JWT, RBAC + tenant isolation, device mTLS, TLS 1.3, AES-256 at rest, vault, OWASP Top 10 + API Top 10 | `docs/security/` |
| Compliance | audit every data access and agent action, location masking, retention, right to erasure (GDPR, DPDP) | `tests/compliance/` |

Required deliverables: Solution Document (template), README, one-command `docker compose up`, seeded 100K dataset, C4 + ER (3NF) diagrams, 3–5 ADRs, test evidence (coverage, load, security scans, CI link), DevOps pack (Dockerfiles, Helm, Terraform, STRIDE), algorithms + SQL write-up with before/after plans, a demo video of at most 5 minutes, and the final tag **`v1.0-submission`**.

---

## 3. Architecture

### 3.1 Services and responsibilities

| Service | Lang | Responsibility | Scales by |
|---------|------|----------------|-----------|
| `simulator` | Go | 100K+ EVs in 3 simulated OEM formats; trips, charging, battery ageing, fault injection, noise | goroutines / replicas (VIN range sharding) |
| `ingest-gateway` | Go | MQTT shared-subscription consumer + HTTPS batch endpoint (OEM-cloud push); auth, schema validation, VIN/DTC parsing, **OEM adapters → canonical Protobuf**, dedup, DLQ, back-pressure | stateless replicas |
| `stream-processor` | Go | Event-time windows per VIN; real-time battery safety rules; charge-session detection; 1-min rollups; writes to ScyllaDB and Redis; emits alerts | Kafka partitions (VIN-keyed) |
| `fleet-api` | Python / FastAPI | Public REST + WebSocket/SSE; tenants, fleets, vehicles, alerts, audit; CQRS read side; alert persistence consumer; outbox relay for its own events (dispatch-optimizer relays its own plan outbox) | stateless replicas |
| `battery-intel` | Python / FastAPI | SoH estimation, RUL / fault-risk model serving, DTC decoding, fault-signature embeddings + similarity | stateless replicas |
| `dispatch-optimizer` | Python / FastAPI | Tariffs, chargers, depot schedule DP, en-route routing (A*), fleet assignment, plan versioning | stateless replicas + job queue |
| `copilot-agent` | Python / LangGraph | LLM agent over MCP tools; RAG over runbooks; approval workflow; audit | stateless replicas |
| `web` | TypeScript / React | Ops console | CDN / static |
| `batch/` | PySpark | Kafka → Iceberg archive, nightly SoH, feature build, fleet reports | Spark executors |

Infrastructure: Kafka (KRaft, Strimzi on K8s), Apicurio Schema Registry, MQTT broker (Mosquitto in dev; EMQX for a clustered K8s deployment, but check its current licence terms and declare them), PostgreSQL 16 + pgvector, ScyllaDB (Cassandra-compatible), Redis 8 (includes Bloom), MinIO / S3 + Apache Iceberg, Keycloak (OIDC), Vault or OpenBao, OpenTelemetry Collector → Prometheus + Grafana + Loki + Tempo.

### 3.2 Event path (and the latency budget for Solution Doc §5.1)

```
Vehicle/OEM cloud ──MQTT(mTLS, QoS1) / HTTPS batch──▶ ingest-gateway      (~20 ms)
   ──Kafka telemetry.canonical.v1 (key=VIN)──▶ stream-processor             (~50–200 ms)
       ├─▶ Redis vehicle:{vin}:state  ──▶ fleet-api WS/SSE ──▶ web map      (total < 2 s)
       ├─▶ ScyllaDB telemetry_raw (hot, 7 d TTL)
       ├─▶ Kafka alerts.v1 ──▶ fleet-api (persist + push)                   (total < 5 s)
       └─▶ Kafka telemetry.rollup.1m.v1
   ──Spark Structured Streaming──▶ Iceberg on S3/MinIO (warm/cold) ──▶ nightly SoH / features / reports
```

### 3.3 Kafka topics

| Topic | Key | Partitions (prod / dev) | Retention | Notes |
|-------|-----|-------------------------|-----------|-------|
| `oem.raw.<oem>.v1` | VIN | 48 / 6 | 3 d | untouched payloads, used for replay after adapter bug fixes |
| `telemetry.canonical.v1` | VIN | 48 / 6 | 7 d | Protobuf, schema registry |
| `telemetry.dlq.v1` | VIN or `unknown` | 6 / 1 | 14 d | reason header + original bytes |
| `telemetry.rollup.1m.v1` | VIN | 24 / 3 | 7 d | |
| `battery.sessions.v1` | VIN | 24 / 3 | 30 d | charge / drive session start/stop (event-sourced) |
| `vehicle.state.v1` | VIN | 48 / 6 | compacted | latest state; rebuilds Redis after loss |
| `fleet.vehicle.v1` | VIN | 48 / 6 | compacted | vehicle registry (tenant, fleet, depot, duty); owned by fleet-api, ADR-0006 |
| `alerts.v1` | VIN | 12 / 3 | 30 d | |
| `dispatch.commands.v1` | depot_id | 12 / 3 | 30 d | published through the outbox only |
| `audit.v1` | tenant_id | 12 / 3 | 400 d → archived | append-only |

Producers use `enable.idempotence=true` and `acks=all`. Replication factor is 3 in K8s and 1 in dev compose.

### 3.4 Delivery semantics

We are **at-least-once end to end, and effectively-once through idempotent sinks**. Document this in an ADR.

- ScyllaDB primary key `((vin, day), ts, seq)` → re-inserts are upserts.
- Postgres alerts use unique `(vin, rule_id, window_start)` with `ON CONFLICT DO NOTHING`.
- Dedup at the gateway: a Bloom filter on `(vin, seq)` catches duplicates; a Redis `SET NX EX 600` confirms a Bloom hit before the event is dropped (so a false positive never loses data).
- Offsets are committed only after sink writes succeed.

---

## 4. Canonical telemetry event

Source of truth: `libs/proto/kilowatt/telemetry/v1/telemetry.proto` (managed with `buf`; run `buf breaking` in CI). Only backward-compatible evolution is allowed: add fields, never renumber or reuse.

```proto
message TelemetryEvent {
  string event_id        = 1;  // UUIDv7
  string vin             = 2;  // validated, 17 chars, check digit
  string oem             = 3;  // "oem_a" | "oem_b" | "oem_c"
  int64  ts_event_ms     = 4;  // vehicle clock (UTC)
  int64  ts_ingest_ms    = 5;  // gateway clock
  uint64 seq             = 6;  // per-vehicle monotonic counter
  double lat = 7; double lon = 8; float speed_kmh = 9; double odo_km = 10;
  float soc_pct          = 11;
  float pack_voltage_v   = 12;
  float pack_current_a   = 13; // + = discharge, - = charge (fixed convention)
  float pack_temp_min_c  = 14; float pack_temp_max_c = 15;
  uint32 cell_v_min_mv   = 16; uint32 cell_v_max_mv  = 17;
  float isolation_kohm   = 18;
  bool  hv_interlock_ok  = 19;
  float aux_12v_v        = 20;
  float ambient_c        = 21;
  ChargeState charge_state = 22; // IDLE, AC, DC_FAST, FAULT
  float charge_power_kw  = 23;
  repeated string dtc    = 24; // validated ^[PCBU][0-3][0-9A-F]{3}$
  EventType evt          = 25; // PERIODIC, IGN_ON, IGN_OFF, PLUG_IN, PLUG_OUT, HARSH_BRAKE, ...
  uint32 schema_version  = 26;
}
```

**Field conventions (everywhere):** units go in names (`_kw`, `_kwh`, `_pct`, `_c`, `_mv`, `_ms`, `_km`). All timestamps are UTC. Money is stored as **integer paise** (`amount_paise`) and never as a float. Always distinguish event time from ingest time.

### 4.1 The three simulated OEM formats (drive the Adapter pattern)

| OEM | Transport | Format quirks |
|-----|-----------|---------------|
| `oem_a` | MQTT `v1/oem_a/{vin}/telemetry` | JSON, metric units, ISO-8601 timestamps |
| `oem_b` | HTTPS batch webhook | JSON arrays, **Fahrenheit**, **miles**, epoch seconds, nested `battery{}` object, SoC 0–1 |
| `oem_c` | MQTT `v1/oem_c/{vin}/t` | compact Protobuf/CSV-like, DTCs as raw hex bytes that must be parsed |

Adding an OEM means adding one adapter class, its contract tests, and a registry entry, with **no downtime** (adapters are selected by topic or header, and unknown formats go to the DLQ).

---

## 5. Data architecture

### 5.1 Polyglot map and CAP choices

| Data | Store | CAP / PACELC | Why |
|------|-------|--------------|-----|
| Tenants, users, roles, fleets, depots, vehicles, packs, drivers, chargers, tariffs, subscriptions, dispatch plans, alerts, audit log, outbox | **PostgreSQL 16** | **CP** (PC/EC) | ACID, 3NF, RLS for tenant isolation, money and plans must be consistent |
| Runbook chunks + fault-signature vectors | **pgvector** (in Postgres) | CP | small corpus (<10M vectors), joins with tenant/vehicle, one less system to run. Revisit Qdrant only if recall or latency fails (ADR) |
| Raw telemetry, hot 7 days | **ScyllaDB** | **AP** (PA/EL), write CL=ONE, read LOCAL_ONE | 100K+ writes/s, partition `(vin, day)` avoids hot spots and unbounded partitions, TTL |
| Latest vehicle state, charger availability, rate-limit buckets, dedup | **Redis 8** | AP, rebuildable | sub-ms reads for the map; rebuilt from compacted `vehicle.state.v1` |
| Warm/cold history, features, rollups | **Iceberg on S3/MinIO (Parquet, zstd)** | eventual (batch) | cheap, cloud-agnostic, Spark/DuckDB scans over billions of rows |
| Stream of record | **Kafka** | CP per partition (min.insync.replicas=2) | replay, ordering per VIN |

### 5.2 Relational core (3NF) — keep `docs/er/` in sync

`tenant`, `app_user`, `role`, `user_role`, `subscription`, `fleet`, `depot`, `vehicle_model` (nominal_capacity_kwh, max_ac_kw, max_dc_kw, chemistry), `battery_pack` (pack serial, model, install_date), `vehicle` (vin, fleet_id, model_id, current_pack_id), `vehicle_duty` (vehicle_id, depot_id, depart_at, return_at, required_soc_pct / planned_km — the optimiser's departure constraints), `driver` (pii encrypted, `driver_ref` pseudonym), `charger_site` (depot or public, lat/lon, geohash, site_power_cap_kw), `charger` (site_id, connector_type, max_kw, ocpp_id), `tariff`, `tariff_window` (tariff_id, dow, start_min, end_min, price_paise_per_kwh), `dtc_code` (code, system, severity, description, runbook_id), `vehicle_dtc_event`, `soh_estimate` (pack_id, as_of, soh_pct, method, ci_low, ci_high), `charging_session`, `dispatch_plan` (versioned, status DRAFT→APPROVED→PUBLISHED), `dispatch_assignment`, `alert`, `alert_ack`, `audit_log`, `erasure_request`, `outbox`, `runbook_chunk` (embedding vector(384)), `fault_signature` (embedding vector(64)).

- **Schema per service**, one Postgres cluster: `fleet` (fleet-api), `battery` (battery-intel), `dispatch` (dispatch-optimizer). A service reads another service's data only through its API or events, never its schema. Each service's `CLAUDE.md` lists the tables it owns.
- Every tenant-owned table has `tenant_id` and **Row-Level Security** keyed on `current_setting('app.tenant_id')`, which the API sets per transaction.
- Deliberate denormalisation (document each one in `docs/er/denormalisation.md`): `vehicle.latest_soh_pct` (a cache of the newest `soh_estimate`), and the `mv_fleet_battery_summary` materialised view.

### 5.3 SQL optimisation evidence (Solution Doc §5.3)

Pick the three slowest real queries, capture `EXPLAIN (ANALYZE, BUFFERS)` before and after, and store both under `docs/sql-optimisation/`. The expected candidates:

1. At-risk vehicle list per fleet (join `soh_estimate` latest-per-pack) → `DISTINCT ON` + composite index `(pack_id, as_of DESC)`, then a materialised view.
2. Open alerts inbox, paginated → partial index `WHERE status='OPEN'` + **keyset pagination** on `(created_at, id)`.
3. Charger availability at time t for a depot → composite index on `(site_id, start_ts)` over assignments, with the N+1 ORM loop removed (`selectinload`).

### 5.4 Capacity and lifecycle (Solution Doc §5.3 / NFR)

Record the method and numbers in `docs/capacity.md`. Baseline from the brief: 100K vehicles × 1 event/s ≈ 100K events/s; ~1 KB JSON → ~8.6 TB/day raw. Canonical Protobuf plus zstd should cut that by an order of magnitude, so **measure** the real ratio and report it.

- **Hot:** Redis (minutes) and ScyllaDB raw (7 days, TTL).
- **Warm:** Iceberg raw for 90 days, and 1-min rollups for 13 months.
- **Cold:** raw data older than 90 days moves to an archive storage class. Driver-linked fields are crypto-shreddable.
- Compute cost with **current** cloud list prices at the time of writing and cite the pricing page and date.

---

## 6. Simulator (a graded deliverable in its own right)

`services/simulator` must be **deterministic given `--seed`** and must support these options:

- `--vehicles 100000` (seed master data covers ≥100K vehicles across multiple tenants, fleets, depots, and 3–4 vehicle models).
- `--rate-hz` per vehicle, and `--mode mqtt|https|kafka-direct|backfill` (kafka-direct is only for broker-level load tests; backfill writes months of reduced-resolution history straight to Iceberg for ML and batch, and must be labelled as synthetic backfill).
- Per-vehicle RNG is seeded from `hash(seed, vin)`, so the output is identical regardless of shard count. `--demo-inject <vin>:<fault>` triggers a fault on demand for the demo video. Full CLI contract: `services/simulator/CLAUDE.md`.
- **Realism:** Indian city geographies (e.g. Bengaluru, Chennai, Surat), shift patterns, depot returns, AC/DC charging behaviour, ambient temperature by time of day.
- **Battery ageing model (ground truth):** calendar fade ∝ √t, accelerated by temperature (Arrhenius) and high SoC dwell; cycle fade ∝ equivalent full cycles, accelerated by DoD, C-rate, and temperature. Internal resistance grows with fade.
- **Fault injection with precursors**, so that prediction is learnable:
  - cooling degradation → rising `pack_temp_max_c − ambient` over days → `P0A7E` (battery over-temperature)
  - cell drift → widening `cell_v_max_mv − cell_v_min_mv` → `P0A7F` (pack deterioration)
  - insulation wear → falling `isolation_kohm` → `P0AA6` (HV isolation fault)
  - connector issue → intermittent `hv_interlock_ok=false` → `P0A0A`
  - weak aux battery → low `aux_12v_v` → comms-loss U-codes
  - The DTC catalogue lives in `data/reference/dtc_catalogue.csv`. Check every description against SAE J2012 / public sources before citing it in docs, and mark anything synthetic as such.
- **Noise the pipeline must survive:** ~1% duplicates, ~2% out-of-order (up to 30 s late), GPS jitter, sensor dropouts, clock skew, **3× bursts** at shift start and after a simulated network outage (buffered replay), and ~0.1% malformed payloads (bad VIN check digit, invalid DTC, unknown OEM version).
- **Ground truth goes to a separate sink** (`data/ground_truth/*.parquet`: true SoH, injected-fault timestamps) that the production pipeline **never reads**. It is used only by the evaluation code. This is our leakage guard.

---

## 7. Algorithms & data structures (Solution Doc §6.5)

Every algorithm lives in a pure `domain/` module and has unit tests, a complexity annotation in its docstring, a benchmark in `bench/`, and a write-up in `docs/algorithms/`.

| Problem | Algorithm | Complexity | Location |
|---------|-----------|-----------|----------|
| VIN validation | regex `^[A-HJ-NPR-Z0-9]{17}$` + check digit (pos 9, transliteration, weights 8,7,6,5,4,3,2,10,0,9,8,7,6,5,4,3,2; mod 11, 10→`X`) | O(1) | `ingest-gateway/internal/domain/vin` |
| DTC parsing | regex `^[PCBU][0-3][0-9A-F]{3}$`; OEM-C hex bytes → code (2-bit system, 2-bit digit, 3 nibbles) | O(n) | `ingest-gateway/internal/domain/dtc` |
| Dedup | Bloom filter (sized for 1% FPR over a 10-min window, rotating pair) + Redis confirm | O(k) | `ingest-gateway` |
| Top-K DTCs / fleet | Count-Min Sketch + min-heap | O(d) update, O(log K) | `stream-processor` |
| Sliding windows | per-VIN ring buffers, event-time watermark, allowed lateness 30 s | O(1) amortised | `stream-processor` |
| Anomaly | EWMA z-score on temp delta, dT/dt, cell imbalance; rule thresholds from config | O(1) | `stream-processor` |
| SoH (physics) | coulomb counting between rest points: `Q_meas = ∫I dt / ΔSoC`, `SoH = Q_meas / Q_nominal`; 1-D Kalman filter to smooth | O(n) per session | `battery-intel/domain/soh` |
| Charger candidates | geohash (precision 6) k-ring lookup in Redis | O(k) | `dispatch-optimizer` |
| Reachable charger | **A\*** on the road graph with **energy-weighted edges** (kWh from distance, speed, gradient), constrained by usable energy minus a safety buffer; admissible heuristic = haversine × min kWh/km | O(E log V) | `dispatch-optimizer/domain/routing` |
| Depot schedule | **DP** over (15-min slot t, SoC bucket s), actions = power levels; cost = tariff + degradation + unmet-SoC penalty | O(T·S·P) per vehicle (96×101×~5) | `dispatch-optimizer/domain/schedule` |
| Site power cap coupling | **Lagrangian relaxation**: a per-(site, slot) price multiplier is added to the tariff, with subgradient updates until caps hold; fallback is a greedy priority queue by slack | O(iter·N·T·S·P) | same |
| En-route assignment | min-cost assignment (Hungarian / `linear_sum_assignment`), one column per connector slot | O(n³) | `dispatch-optimizer/domain/assign` |
| Trip segmentation | DP / state machine over speed and ignition to split noisy GPS into stops and trips | O(n) | `batch/` |

Always compare against a **naive baseline**: "charge on arrival at max power" for dispatch, and "linear fade by odometer" for SoH. Report the delta.

---

## 8. ML & agentic AI (Solution Doc §11)

### 8.1 Models

| Model | Target | Features | Baseline | Metrics | Split |
|-------|--------|----------|----------|---------|-------|
| SoH regressor (LightGBM) | true SoH (ground truth) | EFC, avg C-rate, time at >80% SoC, temp exposure, IR proxy ΔV/ΔI, coulomb-count SoH | linear by odometer; raw coulomb count | MAE (pp), P90 abs error | **time-based + grouped by vehicle** |
| 7-day battery fault risk (LightGBM) | injected fault within 7 days | 24 h / 7 d windowed stats of temp delta, imbalance, isolation, interlock flaps, DTC history | threshold rules | PR-AUC, recall@precision≥0.5, median lead time (h) | same |

Training code lives in `ml/`, reports go in `ml/reports/` (HTML + JSON), and artefacts are versioned (MLflow or plain files with a hash). **Leakage check:** assert that no feature window overlaps the label window, and never put ground truth in the feature store.

### 8.2 Vector layer

- `runbook_chunk.embedding vector(384)` uses a local sentence-transformers model (e.g. `all-MiniLM-L6-v2`), so there is no external API cost and no data leaves the system. The corpus is **our own synthetic EV service runbooks** in `data/runbooks/*.md`.
- `fault_signature.embedding vector(64)` is a normalised summary of the 48 h pre-fault window. Use kNN (HNSW, cosine) to find similar historical cases.

### 8.3 Fleet Copilot (`copilot-agent`)

- LangGraph agent. Its tools are exposed through an **MCP server** that wraps `fleet-api`, `battery-intel`, and `dispatch-optimizer`.
- Tools: `get_vehicle_status`, `list_active_alerts`, `explain_dtc`, `search_runbooks`, `find_similar_faults`, `get_soh_trend`, `simulate_charging_plan` (read-only), `propose_dispatch_change` (**creates a DRAFT only**).
- **Guardrails:**
  - tenant_id and role come from the caller's JWT on the server side, **never from LLM arguments**
  - a tool allow-list per role
  - telemetry strings and runbook text are treated as untrusted data (prompt-injection defence)
  - Pydantic validation of tool I/O
  - every numeric claim must cite a tool-call id
  - a human approves in the UI before anything is published
- **Audit:** every prompt, tool call, arguments, result hash, approval, token count, cost, and latency goes to `audit_log` and `audit.v1`.
- The LLM provider is configurable (`LLM_PROVIDER`, `LLM_MODEL`, `LLM_API_KEY` via vault). A **deterministic fake LLM** is used in tests and CI.
- Eval set: `ml/copilot_eval/*.yaml` (question → expected tools + facts). Report the pass rate.

---

## 9. APIs

- REST under `/api/v1`, with the OpenAPI spec generated to `docs/api/openapi.yaml` and AsyncAPI for topics in `docs/api/asyncapi.yaml`.
- **Auth:** Keycloak OIDC, JWT bearer, audience-checked. **Roles:** `platform_admin`, `fleet_admin`, `dispatcher`, `technician`, `analyst`, `auditor`.
- **Pagination:** keyset cursors only (`?limit=&cursor=`), never OFFSET on large tables.
- **Errors:** RFC 9457 `application/problem+json`.
- **Rate limiting:** Redis token bucket per tenant + user, returning `429` and `Retry-After`.
- **Idempotency:** `Idempotency-Key` header on POSTs that create plans or approvals.
- **Live:** WebSocket `/ws/v1/fleet/{fleet_id}` (throttled vehicle deltas + alerts), with SSE as a fallback.

Core endpoints (extend as needed and keep the feature matrix in sync):

```
GET  /fleets/{id}/vehicles?soh_lt=&status=&cursor=
GET  /vehicles/{vin}                      # latest state (Redis) + SoH + open faults
GET  /vehicles/{vin}/telemetry?from=&to=&resolution=raw|1m|15m
GET  /vehicles/{vin}/soh                  # history + CI
GET  /vehicles/{vin}/faults/{id}/similar  # pgvector kNN
GET  /dtc/{code}                          # decode + runbook
GET  /alerts?status=OPEN&severity=&cursor=
POST /alerts/{id}/ack
POST /depots/{id}/dispatch-plans          # optimise → DRAFT
POST /dispatch-plans/{id}/approve         # dispatcher only → outbox → dispatch.commands.v1
POST /vehicles/{vin}/charge-recommendation # en-route
GET  /reports/charging-cost?fleet_id=&period=
POST /privacy/erasure-requests            # fleet_admin; drives crypto-shredding
GET  /audit?actor=&resource=&cursor=      # auditor
```

---

## 10. Web UI (`web/`)

React + TypeScript + Vite, TanStack Query, MapLibre GL (open tiles), and ECharts or Recharts. Pages:

1. **Fleet live map.** Vehicles coloured by SoC/SoH/alert, clustered at scale, updated over WebSocket.
2. **Battery health.** SoH distribution, the at-risk list (7-day fault probability with the top contributing features), SoH trend per model.
3. **Vehicle detail.** SoC/temperature/imbalance charts, DTC timeline, decoded fault + runbook, "similar past faults".
4. **Charging dispatch planner.** Depot Gantt (chargers × time), site load curve vs power cap, cost vs the "charge-on-arrival" baseline, and an approve button.
5. **Alerts inbox.** Severity, ack, links to the vehicle.
6. **Copilot.** Chat with tool-call trace and approval cards.
7. **Admin / audit.** Users and roles, audit log viewer, erasure requests.

Location masking: roles without `location:precise` see geohash precision 5 (~5 km cells) and no raw coordinates. Enforce this **in the API**, not only in the UI.

---

## 11. Security & compliance (Solution Doc §8)

- **STRIDE** threat model for the ingestion path and the public API: `docs/security/stride.md` (top 5 threats → controls → tests).
- **Device identity:** X.509 certificates from a dev CA (`infra/pki/`), with mTLS on MQTT. Two identity types:
  - **per-vehicle** (demo subset): the gateway verifies that cert CN = the VIN in the topic and payload
  - **OEM-cloud** (bulk, device-free model): CN = OEM id, and that OEM must be registered for the VIN

  Anything else goes to the DLQ as `IDENTITY_MISMATCH` (anti-spoofing).
- TLS 1.3 in transit. AES-256 at rest (volume encryption + Postgres column encryption for driver PII via the vault transit engine).
- **Crypto-shredding erasure:** each driver's PII and `driver_ref` mapping is encrypted with a per-driver data key. Erasure deletes the key, deletes the Postgres rows, and writes a tombstone to the compacted topics. Immutable Parquet/Kafka history only holds the unlinkable pseudonym. `tests/compliance/` proves this end to end.
- **Audit:** every read of vehicle/driver data and every agent action goes to `audit_log` (append-only; the DB role cannot UPDATE or DELETE).
- **Retention:** enforced by TTL (Scylla), Kafka retention, and Iceberg snapshot expiry, all documented in `docs/security/retention.md`.
- OWASP Top 10 and API Security Top 10 checklist in `docs/security/owasp.md`, covering BOLA (object-level auth on every `{vin}`/`{id}`), mass assignment (explicit DTOs), and SSRF (no user-supplied URLs).

---

## 12. Repository layout

```
.
├── CLAUDE.md                  # this file
├── README.md                  # problem, architecture image, quick start, env vars, tests, known issues
├── Makefile                   # the only supported entry points (see §13)
├── .env.example
├── services/
│   ├── simulator/             # Go   cmd/ internal/{domain,app,adapters}
│   ├── ingest-gateway/        # Go
│   ├── stream-processor/      # Go
│   ├── fleet-api/             # Py   app/{api,application,domain,infrastructure} + main.py
│   ├── battery-intel/         # Py
│   ├── dispatch-optimizer/    # Py
│   └── copilot-agent/         # Py
├── web/                       # React + TS
├── batch/                     # PySpark jobs (archive, soh_nightly, features, reports)
├── ml/                        # training, evaluation, reports, copilot_eval
├── libs/
│   ├── proto/                 # canonical schemas (buf)
│   ├── go-common/             # otel, config, kafka, health
│   └── py-common/             # auth/JWT, tenancy/RLS, problem-details, otel, pagination
├── data/
│   ├── reference/             # dtc_catalogue.csv, wmi_synthetic.csv, tariffs, vehicle_models
│   ├── runbooks/              # synthetic service runbooks (RAG corpus)
│   ├── seed/                  # generated 100K master data (git-LFS or regenerated by `make seed`)
│   └── ground_truth/          # simulator truth — NEVER read by the pipeline (gitignored)
├── infra/
│   ├── compose/               # docker-compose.yml (+ .load.yml, .obs.yml overrides)
│   ├── helm/kilowatt/         # umbrella chart; values-aws.yaml, values-azure.yaml, values-local.yaml
│   ├── terraform/{aws,azure}/ # cluster, object storage, registry, DNS (at least AWS)
│   ├── pki/                   # dev CA scripts (no private keys committed)
│   └── observability/         # otel-collector, prometheus rules, grafana dashboards (JSON)
├── tests/
│   ├── bdd/                   # behave features for key user stories
│   ├── contract/              # Pact (web↔fleet-api, fleet-api↔battery-intel/dispatch)
│   ├── load/                  # k6 (API) + Go load driver (100K eps), soak profile
│   ├── chaos/                 # kill broker/pod scripts + assertions
│   ├── compliance/            # audit trail + erasure end-to-end
│   └── security/              # ZAP config, semgrep rules
└── docs/
    ├── adr/                   # 0001-*.md … (Context → Options → Decision → Consequences)
    ├── architecture/          # C4 L1/L2 (Structurizr or Mermaid), sequence diagrams, deployment
    ├── er/                    # 3NF ER diagram + denormalisation notes
    ├── algorithms/            # pseudocode, complexity, measured runtimes
    ├── sql-optimisation/      # EXPLAIN ANALYZE before/after
    ├── security/              # stride.md, owasp.md, retention.md
    ├── api/                   # openapi.yaml, asyncapi.yaml
    ├── evidence/              # load/, coverage/, scans/, chaos/ (raw outputs + hardware notes)
    ├── capacity.md
    ├── feature-matrix.md      # F-xx ↔ code path ↔ tests ↔ video timestamp
    ├── demo-script.md
    ├── declarations.md        # OSS + licences, AI tools used and for what, data statement
    └── solution-document/     # section drafts mirroring the official template
```

### Internal layering (every service)

| Layer | Does | Must not |
|-------|------|----------|
| `api/` (Presentation) | HTTP/WS, DTO validation, auth checks, mapping | contain business rules or SQL |
| `application/` | use cases, transactions, orchestration | depend on a concrete DB or broker (use ports) |
| `domain/` | entities, value objects, pure algorithms | import frameworks or infrastructure |
| `infrastructure/` | repositories, Kafka/Redis/Scylla/S3 clients, external APIs | leak vendor types into the domain |

The composition root (`main.py` / `cmd/*/main.go`) wires adapters to ports. Enforce the rules with `import-linter` (Python) and `depguard` (Go) in CI.

---

## 13. Commands

The Makefile is the contract. If a target below does not exist yet, **create it** instead of running ad-hoc commands, and keep the README in sync.

```bash
make up            # docker compose up -d --build: full stack + seed + simulator (default 100K vehicles @ low Hz)
make down          # stop and remove volumes
make seed          # generate 100K-vehicle master data → Postgres (deterministic SEED)
make sim VEHICLES=100000 RATE_HZ=1 MODE=mqtt
make fmt lint      # ruff+black / gofmt+golangci-lint / eslint+prettier; mypy --strict; buf lint
make test          # unit tests, all services, with coverage gates
make test-int      # Testcontainers: Kafka, Postgres, Redis, Scylla, MinIO
make test-contract # Pact
make test-bdd      # behave
make load          # 100K eps ingest + k6 API profile; writes docs/evidence/load/<date>/
make soak          # long-running profile (default 1 h)
make chaos         # kill a Kafka broker and an API pod; assert no loss + recovery time
make compliance    # audit + erasure end-to-end
make scan          # semgrep, trivy (fs + images), gitleaks, ZAP baseline against local stack
make ml-train ml-eval
make docs          # render diagrams, regenerate OpenAPI/AsyncAPI
make helm-local    # deploy to kind/k3d with values-local.yaml
```

Local UIs (dev defaults, which the README should document): web `:5173`, fleet-api docs `:8000/docs`, Grafana `:3000`, Keycloak `:8080`, MinIO console `:9001`.

**Laptop vs cluster:** 100K vehicles at 1 Hz will not fit on a laptop. The local default runs 100K vehicles at a reduced rate (e.g. 0.1 Hz ≈ 10K eps). The **100K eps benchmark runs on the cloud cluster** (Terraform + Helm), and the evidence records node types and counts. Say so honestly in the Solution Document.

---

## 14. Testing standards (CI gates)

| Suite | Tool | Gate |
|-------|------|------|
| Unit | pytest + pytest-cov; `go test -cover`; vitest | **≥80% line coverage** on `domain/` + `application/` for each service; algorithms ≥90% |
| Property-based | hypothesis / Go fuzz | VIN, DTC parsers, DP schedule invariants (SoC never below the floor, never above the cap, site cap respected) |
| Integration | Testcontainers (Kafka, Postgres+pgvector, Redis, Scylla, MinIO) | must pass |
| Contract | Pact (consumer-driven) + `buf breaking` | must pass |
| Acceptance | behave (Gherkin) | key stories in §15 all green |
| Load / soak | Go load driver + k6 | report throughput, p95/p99, consumer lag |
| Security | Semgrep (SAST), OWASP ZAP (DAST), Trivy (deps + images), gitleaks | 0 critical; highs triaged in `docs/evidence/scans/` |
| Chaos / compliance | scripts in `tests/chaos`, `tests/compliance` | no data loss; recovery time recorded; erasure proven |

**Edge cases that must have tests:** duplicate events, out-of-order and late events beyond allowed lateness, unknown OEM format / version, invalid VIN check digit, malformed DTC, broker outage (gateway back-pressure and MQTT PUBACK withholding), Redis loss (state rebuild from the compacted topic), optimiser timeout (circuit breaker → greedy fallback), a vehicle that cannot reach any charger (alert, not crash), a tariff window crossing midnight, DST-free IST vs UTC handling, and cross-tenant access attempts (must return 404, not 403, to avoid enumeration).

CI (GitHub Actions) runs on every push: lint → unit → build images → integration → contract → BDD → scans → (nightly) load-smoke + chaos. Publish coverage and scan reports as artefacts and link them in the Solution Document.

---

## 15. Feature list (seed for `docs/feature-matrix.md`)

| ID | Feature | User story (short) | MoSCoW |
|----|---------|--------------------|--------|
| F-01 | 100K-EV simulator with ageing, faults, noise | As an evaluator I can reproduce realistic fleet data from a seed | Must |
| F-02 | Multi-OEM ingestion & normalisation | As a platform team I can onboard an OEM format without downtime | Must |
| F-03 | Schema validation, VIN/DTC parsing, dedup, DLQ | As ops I trust that bad data never corrupts analytics | Must |
| F-04 | Live fleet map (< 2 s) | As a dispatcher I see every vehicle's SoC and location live | Must |
| F-05 | Real-time battery safety alerts (< 5 s) | As a technician I'm alerted to thermal, isolation, or interlock faults | Must |
| F-06 | DTC decoding + runbook diagnostics (RAG) | As a technician I know what a code means and what to do | Must |
| F-07 | SoH estimation (coulomb counting + Kalman) | As a fleet manager I see each pack's health with a confidence interval | Must |
| F-08 | Depot charging optimiser (DP + TOU + site cap) | As a dispatcher I get the cheapest plan that meets every departure | Must |
| F-09 | En-route charger dispatch (A* + assignment) | As a driver low on charge I'm sent to a reachable, free charger | Must |
| F-10 | Secure multi-tenant API (OIDC, RBAC, RLS, rate limit, keyset) | As a tenant my data is isolated | Must |
| F-11 | Audit log, location masking, crypto-shred erasure | As a DPO I can prove compliance | Must |
| F-12 | Batch analytics on Iceberg (SoH trends, cost reports) | As finance I see monthly charging cost and savings | Must |
| F-13 | Observability dashboards | As SRE I find the cause of a latency spike | Must |
| F-14 | SoH ML model vs baseline | As a lessor I get more accurate SoH than the odometer heuristic | Should |
| F-15 | 7-day fault-risk prediction | As a manager I service vehicles before they break down | Should |
| F-16 | Health-aware charging cost | As a manager my plans don't trade battery life for pennies | Should |
| F-17 | Fault-signature similarity (pgvector) | As a technician I see similar past cases | Should |
| F-18 | Fleet Copilot with approval + audit | As a dispatcher I ask questions in plain language and approve suggested actions | Should |
| F-19 | Charging carbon report (kWh × grid factor) | As a sustainability lead I see CO₂ per route | Could |
| F-20 | V2G / bidirectional charging | — | Won't (this hackathon) |

Build order (by dependency): F-01 → F-02/03 → F-04/05 → F-07 → F-08 → F-10/11 → F-12/13 → F-06 → F-09 → F-14/15 → F-16/17 → F-18 → F-19.

---

## 16. Architecture Decision Records (minimum set, `docs/adr/`)

1. **ADR-0001** Kafka (KRaft) over RabbitMQ: replay, VIN-partitioned ordering, compacted state topics.
2. **ADR-0002** Polyglot persistence: Postgres (CP) + ScyllaDB (AP) + Redis + Iceberg; why not a single SQL DB (write amplification, contention, mixed workloads).
3. **ADR-0003** pgvector over a dedicated vector DB (corpus size, tenant joins, ops cost), with a revisit trigger.
4. **ADR-0004** At-least-once + idempotent sinks vs Kafka transactions (exactly-once): the CAP/PACELC trade-off per data class.
5. **ADR-0005** Go for the hot path, Python for analytics/ML/API; the hexagonal internals and the language boundary is the canonical Protobuf.

Add an ADR whenever you introduce a new store, broker, language, or cross-service contract.

---

## 17. Design patterns we use (Solution Doc §6.3), with their code locations

Adapter (OEM payloads → canonical), Strategy (optimiser: DP / Lagrangian / greedy fallback; SoH method), Repository (all persistence), **Transactional Outbox** (dispatch approval → Kafka), **CQRS** (Postgres writes; Redis/Scylla read models), **Event Sourcing** (charging/driving sessions), Circuit Breaker + Retry with jitter (API → optimiser/battery-intel; LLM calls), Observer/pub-sub (alerts → WS), Chain of Responsibility (gateway validation pipeline), Factory (adapter registry). Keep this list honest: only list a pattern once it exists in code.

---

## 18. Observability (Solution Doc §10)

- OpenTelemetry SDK in every service. Propagate trace context **through Kafka headers**, so a single trace spans gateway → processor → alert → WS push.
- Structured JSON logs with `trace_id`, `tenant_id`, and `vin` (masked for non-privileged sinks) → Loki.
- Required Grafana dashboards (JSON committed): ingest eps, consumer lag per group, e2e latency histogram (event ts → WS push), alert latency, API p95/p99 per route, error rate, DLQ rate by reason, optimiser runtime, LLM tokens/cost.
- Keep an SLO + alert rules file. Write a troubleshooting walk-through for a latency spike (metrics → exemplar trace → logs) in `docs/observability.md`.

---

## 19. DevOps

- One Dockerfile per service: multi-stage, distroless/slim, non-root, pinned digests, healthchecks.
- Helm umbrella chart. HPA on CPU and on Kafka lag (KEDA), PodDisruptionBudgets, anti-affinity, NetworkPolicies, and resource requests/limits.
- Terraform for **AWS** (EKS, S3, ECR) at minimum, plus a second cloud module (Azure AKS + Blob via an S3-compatible gateway, or GCP). **The only difference between clouds is Helm values and Terraform**, never code.
- Kafka runs on Strimzi inside the cluster (not a managed vendor-only service) to keep portability, and the ADR notes the trade-off.
- The final commit is tagged `v1.0-submission`. Only commits before the deadline count.

---

## 20. Demo video plan (≤ 5:00; the template's segments)

| Time | Show |
|------|------|
| 0:00–0:30 | The dispatcher's pain: a depot of EVs all plugging in at 6 pm peak tariff, plus one surprise battery fault. One number that proves it matters. |
| 0:30–1:00 | The pitch: "Kilowatt tells you which batteries are failing and charges your fleet at the lowest total cost, without missing a departure." |
| 1:00–3:00 | Live on the 100K stream: the map → an injected thermal fault fires an alert in < 5 s → the vehicle page shows the decoded DTC, runbook, and similar past faults → the Copilot answers "which vehicles at Depot X are at risk this week?" → the dispatcher generates a depot plan (Gantt, load under cap, ₹ saved vs baseline) → approve. |
| 3:00–4:15 | Under the hood: architecture, the Grafana dashboard at load (eps, lag, p99), **kill a Kafka broker live** and show recovery with no loss. |
| 4:15–5:00 | Measured results (cost saving %, SoH MAE, fault recall and lead time, latency), next steps, and the team. |

Keep `docs/demo-script.md` and the feature-matrix video timestamps in sync.

---

## 21. Impact metrics (Solution Doc §2.3)

Replace the targets with measured values from `docs/evidence/`. Never ship a target presented as a result.

| Metric | Baseline | Target | How measured |
|--------|----------|--------|--------------|
| Charging energy cost / vehicle-day | charge-on-arrival | ≥15% lower | simulation back-test over 30 simulated days |
| Depot peak load vs site cap | uncontrolled | always ≤ cap | optimiser output + simulation |
| Missed departures (SoC below required) | — | 0 | simulation |
| SoH estimation error | odometer-linear | MAE ≤ 2 pp | vs ground truth |
| Battery fault prediction | threshold rules | recall ≥ 0.7 @ precision ≥ 0.5, ≥ 48 h lead | vs injected faults |
| Critical alert latency | — | p95 < 5 s | OTel |

---

## 22. How to work in this repo (for Claude and humans)

- **Before coding:** read the relevant section here, the service README, and the related ADRs. For a non-trivial change, write a short plan in the PR description first.
- **Definition of Done for a change:** code, tests (unit + integration if it touches I/O), lint/type-check clean, docs updated (feature matrix, OpenAPI/AsyncAPI if contracts changed, ADR if architectural), and CI green.
- **Commits:** Conventional Commits (`feat(dispatch): …`, `fix(gateway): …`). Keep PRs small. Every team member must have commits.
- **Python:** 3.12, FastAPI, SQLAlchemy 2 / SQLModel, Pydantic v2, `ruff` + `mypy --strict`, async I/O, `confluent-kafka`.
- **Go:** 1.24+ (parquet-go needs it), `franz-go` for Kafka, `golangci-lint`, context everywhere, no global state.
- **TS:** strict mode, eslint + prettier, and no `any` without a comment.
- **Performance hygiene:** batch Kafka produce/consume, reuse connections, avoid ORM N+1 queries (verify with query logging in tests), and never do per-event network calls in the hot path when a batch or cache works.
- **When unsure between two designs**, pick the simpler one that still meets the NFR. Record the alternative in an ADR's "Options considered".
- **Don't** add a new datastore, broker, or language without an ADR. Don't read `data/ground_truth/` outside `ml/` evaluation code. Don't put real personal data anywhere. Don't mark a feature Done without a demo path.
- **AI assistance:** when Claude generates substantial code or docs, note it in `docs/declarations.md` (component + nature of help). The team owns review and correctness.

---

## 23. Solution Document mapping (template section → source of truth)

| Template § | Pull from |
|------------|-----------|
| 1 Executive Summary | §1, §21 measured results |
| 2 Problem & Validation | §1.1, `docs/solution-document/02-evidence.md` (facts vs assumptions table with sources) |
| 3 Solution / Value / Innovation | §1.3, UI screenshots |
| 4 Feature List | `docs/feature-matrix.md` |
| 5 HLD | `docs/architecture/`, §5, `docs/capacity.md`, `docs/sql-optimisation/`, deployment diagram |
| 6 LLD | §12 layering, §17 patterns, `docs/api/`, sequence diagrams (happy path + broker-down / duplicate / OEM format change), `docs/algorithms/` |
| 7 NFR & Benchmarks | `docs/evidence/load/` |
| 8 Security | `docs/security/` |
| 9 Test Strategy | CI artefacts, `docs/evidence/` |
| 10 Observability | dashboard screenshots, `docs/observability.md` |
| 11 AI/ML | `ml/reports/`, copilot eval |
| 12 ADRs / Risks / Future | `docs/adr/`, `docs/risks.md` |
| 13 Demo | `docs/demo-script.md` |
| 14 Repo checklist | README, Makefile, CI, `.env.example`, tag |
| 16 Declarations | `docs/declarations.md`, SBOM (`syft` → `sbom.spdx.json`) |

---

## 24. Glossary (project-specific)

**SoC**: State of Charge (%). **SoH**: State of Health = current usable capacity / nominal capacity. **RUL**: remaining useful life. **EFC**: equivalent full cycles. **C-rate**: charge/discharge current relative to capacity. **DoD**: depth of discharge. **TOU**: time-of-use tariff. **IR**: internal resistance. **HV interlock**: safety loop that confirms HV connectors are mated. **Isolation resistance**: resistance between the HV system and the chassis; low values are a safety fault. **OCPP**: Open Charge Point Protocol (charger ↔ backend). **DTC**: Diagnostic Trouble Code (SAE J2012 format). **Crypto-shredding**: erasing data by destroying its encryption key.

---

## 25. Claude Code setup in this repo

- **Per-area guidance:** each of `services/*/`, `web/`, `batch/` and `ml/` has its own `CLAUDE.md`, which loads when Claude works in that folder. Keep details there, and keep this file for cross-cutting rules.
- **`.claude/settings.json`**:
  - pre-approves routine commands (make, tests, linters, docker compose, read-only git/kubectl)
  - asks before push, tag, apply, install or delete
  - denies reading `.env`, private keys and `data/ground_truth/`

  Personal overrides go in `.claude/settings.local.json` (gitignored).
- **Hooks:**
  - `.claude/hooks/guard.py` (before every tool call) blocks ground-truth access, `.env` reads, private keys and secret-looking content
  - `.claude/hooks/post_edit.py` (after every edit) formats the file (ruff / goimports / prettier + eslint / buf / terraform fmt) and reports lint errors and layering violations back to Claude

  Both are stdlib Python 3 and skip tools that aren't installed.
- **Skills:**
  - `/new-adr <title>` writes an ADR in the template format
  - `/feature-done F-xx` runs the Definition-of-Done gate and updates the feature matrix and declarations
  - `/evidence <kind> [label]` runs a benchmark or scan and stores raw output, hardware info and a summary judged against the NFRs
- **Subagents:**
  - `test-writer` writes tests against the must-cover catalogue
  - `reviewer` does a read-only review of the diff against this file's rules; run it before every commit

---

### Team placeholders (fill in)

- Team name: `Amazing Coders`
- Members & roles: `[name — role — email]`
- Repository URL: https://github.com/g-s-jithesh/Amazing-Coders · CI: https://github.com/g-s-jithesh/Amazing-Coders/actions · Demo video: https://drive.google.com/file/d/15A3AiAy_CPrZ7cfc7xiuHUurIiqqgJeO/view?usp=sharing
