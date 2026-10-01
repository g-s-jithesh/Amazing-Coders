# ADR-0001: Kafka (KRaft) over RabbitMQ

- **Status:** Accepted
- **Date:** 2026-10-01
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §3.3, ADR-0004

## Context
Telemetry for 100 K vehicles must be ordered per vehicle, replayable after an adapter bug, and re-readable by
independent consumers (stream processing, SoH worker, raw sink) at their own pace. Latest-state topics must be
rebuildable.

## Options considered
1. **RabbitMQ**: simple routing, per-message acks. Messages are gone once consumed, so no replay and no
   independent consumer positions; ordering per key needs a consistent-hash exchange plus single consumers.
2. **Kafka (KRaft)**: partitioned log, key = VIN gives per-vehicle ordering, consumer groups, retention-based
   replay, compacted topics for latest state (`fleet.vehicle.v1`, `vehicle.state.v1`).
3. **Pulsar / Redpanda**: similar model; no advantage worth a less common operational skill set here.

## Decision
Kafka in KRaft mode (no ZooKeeper), one topic per data class, key = VIN (depot_id for dispatch commands),
producers idempotent with `acks=all`. Single broker in local compose; replication factor 3 and Strimzi in a cluster.

## Consequences
- Replay, fan-out and compacted registries come for free (used by ADR-0006).
- More operational weight than a classic queue. Local single-broker runs showed controller-heartbeat timeouts
  under laptop load (see `docs/evidence/battery/2026-10-01-soh-live/README.md`); a cluster broker count removes this.
