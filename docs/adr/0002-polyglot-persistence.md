# ADR-0002: Polyglot persistence: Postgres, ScyllaDB, Redis, object storage

- **Status:** Accepted
- **Date:** 2026-10-01
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §5.1

## Context
Four access patterns: ACID master data/plans/audit with tenant isolation; 100 K+ time-series writes/s with TTL;
sub-millisecond latest-state reads for a live map; cheap long-term history for batch analytics.

## Options considered
1. **One Postgres for everything**: simplest, but 100 K inserts/s of telemetry means heavy write amplification and
   bloat next to transactional data, and hot reads compete with writes.
2. **Postgres + ScyllaDB + Redis + Iceberg/S3 (chosen)**: each store sized for one pattern.
3. **Cassandra instead of Scylla**: same model, slower per node; ScyllaDB's licence change is declared in
   `docs/declarations.md` (we use the last open-source line, 6.2).

## Decision
Postgres 16 (CP: tenants, vehicles, SoH, plans, outbox, RLS) · ScyllaDB (AP: raw telemetry, partition `(vin, day)`,
TTL) · Redis (rebuildable latest state) · Iceberg on S3/MinIO (warm/cold, not built in this submission).

## Consequences
- Each service owns its Postgres schema (`fleet`, `battery`, `dispatch`) and is reached only through its API/events.
- More moving parts to run; mitigated by one `docker compose` file. The submission implements Postgres, Scylla,
  Redis and Kafka; Iceberg/batch is listed as future work.
