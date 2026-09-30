# Project Kilowatt — Team Amazing Coders

EV fleet battery health, intelligent charging dispatch and fault diagnostics.
Built for the Motorq Connected Vehicle Intelligence Hackathon (not affiliated with Motorq).

> Status: foundation + simulator master data (F-01 partial). See [`docs/feature-matrix.md`](docs/feature-matrix.md) for what is built.

## Quick start

Requires Docker, GNU Make, Go 1.24+.

```bash
cp .env.example .env   # dev-only values
make up                # Kafka (KRaft) + topics, Postgres 16 + pgvector, Redis 8, Mosquitto
make seed              # 100K vehicles of synthetic master data → Postgres (SEED=42 VEHICLES=100000 TENANTS=3)
make sim               # stream 100K vehicles at 0.1 Hz (≈ 10K events/s) in real time; Ctrl-C to stop
make test              # unit tests
make test-int          # integration tests against the running stack
make down              # stop + wipe volumes
```

### Simulator

`make sim` options: `MODE=mqtt` (native transports: oem_a/oem_c over MQTT, oem_b over HTTPS to the gateway) · `https` (all as
signed batches) · `kafka-direct` (raw payloads straight to `oem.raw.*`, broker load tests only) · `RATE_HZ` · `SPEEDUP`
(sim s per wall s; `0` = as fast as possible) · `NOISE=clean|realistic|hostile` · `DURATION=60s` ·
`INJECT=<VIN>:<fault>` (demo fault, 12 h precursor by default: use `SPEEDUP` to compress it).
Until the ingest-gateway exists, oem_b batches are counted as `dropped_records` (expected).
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
