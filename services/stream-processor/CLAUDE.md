# stream-processor — CLAUDE.md

Go service. It is the **real-time brain**: per-VIN event-time processing, battery safety rules (critical alert < 5 s end to end), charge and drive session detection, 1-min rollups, and the hot-path sinks. See root §3.2–3.4 and §7.

## Contracts

- **Consumes:** `telemetry.canonical.v1` (consumer group `stream-processor`, cooperative-sticky assignor).
- **Produces:**
  - `alerts.v1`
  - `battery.sessions.v1` (session start/end summaries; input to SoH estimation in battery-intel)
  - `telemetry.rollup.1m.v1`
  - `vehicle.state.v1` (compacted)
- **Writes:**
  - ScyllaDB `telemetry_raw`, PK `((vin, day), ts_event_ms, seq)`, TTL 7 d
  - Redis `vehicle:{vin}:state` (HASH), `fleet:{fleet_id}:geo` (GEOADD), pub/sub `fleet:{fleet_id}:deltas` (throttled to 1 msg/vehicle/s), and top-K DTCs `topk:{tenant}:{window}` (ZSET)

## Layout

```
cmd/stream-processor/main.go
internal/domain/
  window/     # ring buffers, tumbling/sliding windows, watermark (pure)
  rules/      # Rule interface + implementations; config-driven thresholds
  alert/      # alert state machine (OK → FIRING → RESOLVED) with hysteresis; deterministic IDs
  session/    # charge/drive session state machine; ΔAh via trapezoidal ∫I dt; rest-point capture
  sketch/     # Count-Min Sketch + min-heap top-K
  ewma/       # EWMA mean/variance, z-score
internal/app/ # per-partition processor: state map, event routing, flush/commit orchestration
internal/adapters/ kafka/ scylla/ redis/ config/
config/rules.yaml
```

## Event time and state

- Kafka partitioning by VIN keeps each vehicle's events on one partition, so the **state lives in memory, per partition**.
  - On partition assign: rebuild state from `vehicle.state.v1` (compacted) + Redis.
  - On revoke: flush the sinks, commit, then drop the state.
- Watermark per partition = `max(ts_event_ms) − 30 s` (allowed lateness).
  - Late events **within** lateness update windows normally.
  - **Beyond** lateness they are still written to Scylla (idempotent) but don't reopen closed windows or re-fire alerts. Count them in `late_events_total`.
- Use **event time** for all rules and windows. Use processing time only for timeouts (e.g. "no data for 10 min" → `TELEMETRY_STALE`).

## Rules (thresholds in `config/rules.yaml`, never hard-coded)

| Rule ID | Signal | Default shape | Severity |
|---------|--------|---------------|----------|
| `THERMAL_OVERTEMP` | `pack_temp_max_c` | ≥ threshold for 30 s | critical |
| `THERMAL_RISE_RATE` | dT/dt over a 60 s window | > x °C/min | critical |
| `THERMAL_DELTA_ANOMALY` | `pack_temp_max_c − ambient_c` | EWMA z-score > 4 over 10 min | warning |
| `CELL_IMBALANCE` | `cell_v_max_mv − cell_v_min_mv` | > threshold sustained 5 min | warning → critical |
| `ISOLATION_LOW` | `isolation_kohm` vs pack voltage | below Ω/V limits (derive from UN R100 minimums; verify the figures before citing) | critical |
| `HV_INTERLOCK_OPEN` | `hv_interlock_ok=false` while not ignition-off | 2 occurrences in 5 min | critical |
| `AUX_12V_LOW` | `aux_12v_v` | < threshold for 10 min parked | warning |
| `CHARGE_NEEDED` | SoC vs reserve + distance to depot | below reserve | info (consumed by dispatch-optimizer) |
| `DTC_RAISED` | new code in `dtc[]` | severity from `dtc_catalogue.csv` | per catalogue |
| `TELEMETRY_STALE` | no events | > 10 min while in duty window | warning |

- Alert ID = `uuidv5(ns, vin|rule_id|window_start_ms)`, so a re-delivery produces the same ID and fleet-api's `ON CONFLICT DO NOTHING` makes it idempotent.
- Hysteresis: fire once per episode, and resolve only after the signal has stayed clear for N minutes (configured per rule).
- Alerts carry `evidence` (the window stats that triggered them) for the UI and the copilot.

## Sessions (feed SoH)

A charge session starts on `PLUG_IN`, or when `charge_state != IDLE` and current < 0, and ends on `PLUG_OUT` or idle. It emits:
- `delta_ah`, `delta_kwh`, `soc_start/end`, `v_rest_before/after` (if rest ≥ 30 min before/after)
- `temp_min/max`, `max_c_rate`, `charger_type`

Drive sessions work the same way (distance, energy, kWh/km).

## Sinks and commits

- Scylla: async writes with bounded concurrency. Batch **only within a partition key** (unlogged). Prepared statements. CL = ONE.
- Redis: pipelined HSET + GEOADD per poll batch. Publish deltas at most 1/s per vehicle.
- **Commit offsets only after all sink writes for the batch succeed.** On a sink failure, retry with backoff. If it keeps failing, pause the partition (back-pressure) rather than skipping.
- If Redis is unavailable, the live map degrades but alerts and Scylla continue. Metric + log, no crash.

## Observability

- Consumer lag per partition, events/s, rule evaluations and fires per rule, e2e latency histogram (`now − ts_event_ms` at alert publish), late events, sink latency and errors.
- Continue the trace from the Kafka headers and propagate it onto `alerts.v1`.

## Test focus

- Each rule: at threshold / just below / hysteresis / resolve.
- Deterministic alert IDs on redelivery.
- Out-of-order within vs beyond lateness.
- Partition revoke/assign rebuilds state correctly.
- Session ΔAh against an analytic integral.
- CMS error bound.
- Scylla/Redis outage behaviour (Testcontainers).
- Throughput benchmark per partition.
- An e2e test: canonical event in → alert out within 5 s (Testcontainers Kafka).
