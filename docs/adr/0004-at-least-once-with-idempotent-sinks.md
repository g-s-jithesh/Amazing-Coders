# ADR-0004: At-least-once delivery with idempotent sinks

- **Status:** Accepted
- **Date:** 2026-10-01
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §3.4

## Context
Losing a safety alert is worse than seeing it twice, and Kafka transactions across Kafka, Scylla, Redis and
Postgres are not available end to end.

## Options considered
1. **Kafka exactly-once (transactions)**: only covers Kafka-to-Kafka; sinks outside Kafka still need idempotence.
2. **At-least-once + idempotent sinks (chosen)**: commit offsets only after the sink write; make each sink an upsert.

## Decision
- Scylla primary key `((vin, day), ts, seq)`; Postgres uniqueness `(pack_id, session_id)` for SoH and
  `(depot_id, version)` for plans; deterministic UUIDv5 alert/session ids.
- Gateway dedup: Bloom filter + Redis confirm, a key is remembered only **after** the Kafka ack (a retry after a
  failed produce must not be dropped as a duplicate; this bug was found and fixed during development).
- Outbox relay (dispatch) publishes at least once; consumers dedupe by `plan_id`.
- Consumers store an offset only after the record was handled (battery-intel worker).

## Consequences
- Duplicates can reach consumers after a crash; every consumer must be idempotent (tested: same session twice gives one estimate).
- CAP/PACELC per data class: Postgres CP, Scylla AP (write CL=ONE), Redis AP and rebuildable.
