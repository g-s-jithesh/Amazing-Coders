# Gateway end-to-end accounting — local, aged broker (2026-09-30)

Simulator (100K vehicles, 0.1 Hz, realistic noise, native transports) → Mosquitto / HTTPS → ingest-gateway
(container) → Kafka. Question: is every published record accounted for, and does Kafka hold exactly what the gateway
says it accepted and rejected?

| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Records unaccounted (published − accepted − rejected − duplicates) | 0 | 0 of 1,240,811 | PASS |
| `telemetry.canonical.v1` offset delta = accepted | equal | 1,227,193 = 1,227,193 | PASS |
| `telemetry.dlq.v1` offset delta = rejected | equal | 1,260 = 1,260 | PASS |
| DLQ reasons = only the injected malformed kinds | 5 kinds | DECODE 244, DTC_FORMAT 249, RANGE 277, UNKNOWN_SCHEMA_VERSION 244, VIN_CHECKSUM 246 | PASS |
| Duplicates removed / injected (~1 %) | ≈ 1 % | 0.996 % | PASS |
| Produce latency per message (until all acks), p50 / p95 / p99 | — (alert NFR < 5 s end to end) | ≤ 64 ms / ≤ 256 ms / ≤ 4.1 s | **CONCERN** |

Hardware: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB), 1 Kafka broker.

## Finding: bimodal produce latency

97 % of messages are acknowledged within 256 ms, but ~24K (2.7 %) fall in a separate 1–8 s+ cluster (5,156 over 8 s).
That shape is a stall event, not a tail. Hypothesis: the single local broker, which at this point held ~40M records
from earlier benchmark runs on Docker Desktop's filesystem and showed full 10 s stalls in
`../2026-09-30_sim-kafka-direct-local-2/`. Tested in `../2026-09-30_gateway-e2e-local-2/` on a fresh broker.

## Caveats

- Local laptop, one broker, RF 1, no resource limits; not the 100K eps NFR (that runs on the cluster).
- Latency here is gateway → Kafka ack only, not event → dashboard.

## Reproduce

See `../2026-09-30_gateway-e2e-local-2/summary.md` (same commands); `python summarise.py` recomputes the table.
