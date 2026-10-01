# Project Kilowatt — Team Amazing Coders

EV fleet battery health, intelligent charging dispatch and fault diagnostics.
Built for the Motorq Connected Vehicle Intelligence Hackathon (not affiliated with Motorq).

> Status: simulator (F-01), ingest-gateway (F-02/F-03), stream-processor (F-05, F-04 data side) and battery-intel (F-07 SoH, F-06 DTC decode) and dispatch-optimizer (F-08 depot planner) running end to end. See [`docs/feature-matrix.md`](docs/feature-matrix.md) for what is built.

## Quick start

Requires Docker, GNU Make, Go 1.24+, and [uv](https://docs.astral.sh/uv/) for the Python services' tests.

```bash
cp .env.example .env   # dev-only values
make up                # builds + starts Kafka (KRaft) + topics, Postgres 16 + pgvector, Redis 8, Mosquitto, ScyllaDB, ingest-gateway, stream-processor, battery-intel (API + session worker), dispatch-optimizer (API + outbox relay)
make seed              # 100K vehicles of synthetic master data → Postgres + vehicle registry topic (SEED=42 VEHICLES=100000 TENANTS=3)
make sim               # stream 100K vehicles at 0.1 Hz (≈ 10K events/s) in real time; Ctrl-C to stop
make test              # unit tests
make test-int          # integration tests against the running stack
make e2e OUT=<dir>     # full pipeline run with a demo fault: alert latency, lag, sink timings (write OUT outside OneDrive)
make dispatch-backtest OUT=<file>  # depot planner vs charge-on-arrival, 5 seed depots × 30 days
make down              # stop + wipe volumes
```

### Simulator

`make sim` options: `MODE=mqtt` (native transports: oem_a/oem_c over MQTT, oem_b over HTTPS to the gateway) · `https` (all as
signed batches) · `kafka-direct` (raw payloads straight to `oem.raw.*`, broker load tests only) · `RATE_HZ` · `SPEEDUP`
(sim s per wall s; `0` = as fast as possible) · `NOISE=clean|realistic|hostile` · `DURATION=60s` ·
`INJECT=<VIN>:<fault>` (demo fault, 12 h precursor by default: use `SPEEDUP` to compress it).
The gateway validates everything; rejects land in `telemetry.dlq.v1` with an `x-dlq-reason` header.
Golden OEM payloads live in `libs/oem-samples/` (`make samples` regenerates them).

Trace one simulated vehicle (CSV, includes the simulator-only `soh_true` column):

```bash
cd services/simulator && go run ./cmd/simulator trace --ref ../../data/reference --hours 48 --every 60 > trace.csv
# with a fault: --inject cooling_degradation|cell_drift|insulation_wear|connector_issue|weak_aux_battery --precursor 12h
```

`make seed` needs Go 1.24+. It is deterministic: the same `SEED`/`VEHICLES`/`TENANTS` always produce byte-identical data.

| Service | Address |
|---------|---------|
| Kafka | `localhost:9092` |
| Postgres | `localhost:5432` (db/user `kilowatt`) |
| Redis | `localhost:6379` |
| MQTT (Mosquitto, dev: anonymous, no TLS) | `localhost:1883` |
| ingest-gateway: `POST /ingest/v1/{oem}/batch`, `/healthz`, `/readyz`, `/metrics` | `localhost:8081` |
| stream-processor (processor role) `/metrics` `/readyz` | `localhost:9102` |
| stream-processor (raw-sink role) `/metrics` `/readyz` | `localhost:9103` |
| ScyllaDB (CQL) | `localhost:9042` |
| battery-intel API: `/internal/v1/vehicles/{vin}/soh` (header `X-Tenant-Id`), `/internal/v1/dtc/{code}`, `/docs` | `localhost:8001` |
| battery-intel session worker `/metrics` | `localhost:9104` |
| dispatch-optimizer API: `POST /internal/v1/depots/{id}/dispatch-plans`, `GET /internal/v1/dispatch-plans/{id}`, `POST …/approve` (headers `X-Tenant-Id`, `X-User-Id`, `X-Roles`) | `localhost:8002` |

Watch alerts live: `cd services/stream-processor && go run ./cmd/alerts-tail --vin <VIN>`.

## Layout

- `libs/proto/` — canonical `TelemetryEvent` (buf; `make proto-lint`, `make proto-breaking`)
- `data/reference/` — synthetic WMIs, DTC catalogue
- `infra/compose/` — local stack
- `services/`, `web/`, `batch/`, `ml/` — see each folder's `CLAUDE.md`
- `CLAUDE.md` — architecture and working rules

## Data statement

All data is synthetic. Simulated VINs use WMIs starting with `0`, which is not an assigned
ISO 3780 region, so they can never match a real vehicle. DTC descriptions in
`data/reference/dtc_catalogue.csv` are marked `unverified` until checked against SAE J2012.
