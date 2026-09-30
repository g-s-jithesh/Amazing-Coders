# ADR-0006: Vehicle registry as a compacted Kafka topic

- **Status:** Accepted
- **Date:** 2026-09-30
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §3.3, §5.2 (schema per service), ADR-0001, features F-04, F-05, F-08

## Context

The stream-processor needs each VIN's tenant, fleet, depot location, duty window and battery capacity: the live map is
keyed `fleet:{fleet_id}:geo`, DTC top-K is per tenant, `TELEMETRY_STALE` only applies inside the duty window and
`CHARGE_NEEDED` needs the distance to the depot. The canonical telemetry event carries none of this. The master data is
owned by fleet-api (Postgres schema `fleet`), and root §5.2 says another service may read it only through fleet-api's
API or events. The processor must look this up for every event at ~100K events/s, and its state is rebuilt per Kafka
partition on rebalance.

## Decision drivers

1. Per-event lookup must be in-memory (no network call per event, root §22 performance hygiene).
2. Respect schema ownership (root §5.2): no direct reads of the `fleet` schema.
3. Survive restarts and rebalances without a synchronous dependency on fleet-api being up.
4. Changes (a vehicle moving fleet or depot) must reach the processor within seconds, without redeploys.

## Options considered

| Option | Summary | Pros | Cons |
|--------|---------|------|------|
| A. Compacted topic `fleet.vehicle.v1` (key = VIN), published by fleet-api via its outbox | Processor reads the whole topic at start, then follows it | In-memory lookups; event-driven updates; replayable; no runtime coupling to fleet-api | New contract; ~100K-message startup read |
| B. Processor calls fleet-api `GET /internal/vehicles` at start + polls | Simple REST | Synchronous dependency at startup; polling delay; fleet-api does not exist yet |
| C. Processor reads `fleet.vehicle` from Postgres | Fewest moving parts | Violates schema ownership; couples deploys and migrations |

## Decision

We will use **A: a compacted topic `fleet.vehicle.v1`** (Protobuf `kilowatt.fleet.v1.Vehicle`, key = VIN). It keeps every
lookup in memory, respects schema ownership, and fits the event-driven architecture already used for
`vehicle.state.v1`. Until fleet-api exists, `make seed` publishes the registry from the deterministic master data.

### CAP / PACELC

Registry data is read-mostly and eventually consistent (PA/EL): an event for a VIN whose registry entry has not arrived
yet is still processed; rules that need registry data are skipped and the alert carries an empty tenant, which fleet-api
resolves on persist. The topic itself is CP per partition (RF 3, min.insync.replicas 2 in K8s).

## Consequences

**Positive**
- O(1) in-memory lookups; no per-event network calls.
- A new fleet/depot assignment reaches every processor replica within seconds.
- The same topic can feed dispatch-optimizer and battery-intel.

**Negative / risks → mitigation**
- Startup must read ~100K records before rules have full context → the processor starts consuming telemetry anyway and
  fills the registry concurrently; unregistered VINs are counted in a metric.
- Two writers during bootstrap (seed, later fleet-api) → fleet-api becomes the only writer when it lands; `make seed`
  then stops publishing.
- Deleting a vehicle needs a tombstone (null value) → part of fleet-api's outbox contract.

**Follow-ups**
- [ ] fleet-api publishes `fleet.vehicle.v1` through the transactional outbox on every vehicle/duty change (Step 8).
- [ ] Document the topic in `docs/api/asyncapi.yaml`.

## Evidence

- TBD – startup registry load time and memory measured with `/evidence` in the stream-processor e2e run.

## Revisit trigger

Registry grows past ~5M vehicles per processor replica (memory), or a consumer needs data joins the topic cannot carry.

## Implementation

- `libs/proto/kilowatt/fleet/v1/vehicle.proto`
- `services/simulator` (`seed --publish-registry`), `infra/compose` (topic creation)
- `services/stream-processor/internal/adapters/kafka` (registry loader)
