# Project Kilowatt — Team Amazing Coders

EV fleet battery health, intelligent charging dispatch and fault diagnostics.
Built for the Motorq Connected Vehicle Intelligence Hackathon (not affiliated with Motorq).

> Status: foundation only. See [`docs/feature-matrix.md`](docs/feature-matrix.md) for what is built.

## Quick start

Requires Docker, GNU Make.

```bash
cp .env.example .env   # dev-only values
make up                # Kafka (KRaft) + topics, Postgres 16 + pgvector, Redis 8
make ps
make down              # stop + wipe volumes
```

| Service | Address |
|---------|---------|
| Kafka | `localhost:9092` |
| Postgres | `localhost:5432` (db/user `kilowatt`) |
| Redis | `localhost:6379` |

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
