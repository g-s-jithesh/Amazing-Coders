# battery-intel live SoH run (F-07), 2026-10-01, local laptop

**What it shows:** real charge sessions from the running pipeline (simulator → ingest-gateway →
stream-processor `battery.sessions.v1` → battery-intel worker) turned into coulomb-counting SoH estimates
with a Kalman-smoothed 95 % CI, stored under RLS and served by the internal API. **It does not measure
accuracy:** SoH error against the simulator's truth is evaluated only in `ml/` (leakage guard).

## Setup

- Hardware: Windows 11 laptop, Intel 12-thread CPU (`Intel64 Family 6 Model 154`), 14 GB RAM visible to Docker.
- Stack: `make up` compose (single Kafka broker, Postgres 16, Scylla 6.2, Redis 8), seed `SEED=42 VEHICLES=100000 TENANTS=3`.
- Simulator: `make sim RATE_HZ=0.005 SPEEDUP=24 DT=10 DURATION=14h` (start = wall-clock now, 08:17 IST),
  ~12 K events/s, 100 K vehicles. Raw log: `sim_run3.log` (first/last stats lines kept).

## Results (from the files in this folder)

| | value | source |
|---|---|---|
| SoH estimates stored | 14,872 (14,138 packs) | `estimates_summary.csv` |
| Observed SoH, median (min–max) | 87.85 % (66.31–98.69) | `estimates_summary.csv` |
| Mean 95 % CI width after 1 / 2 sessions | 7.08 pp / 5.28 pp | `ci_width_by_sessions.csv` |
| Rejected by gate (this worker process) | ΔSoC < 20 pp: 19,871 · telemetry gaps: 45,269 · not a charge: 44 | `worker_metrics.txt` |
| Duplicates (redelivery) | 0 | `worker_metrics.txt` |
| API: own tenant 200, other tenant 404 | yes | `api_sample.json` |

The worker counters were reset by one restart (see below), so they cover the part of the run after it;
the DB counts cover the whole run.

## Honest notes

1. **Gap rejections are inflated by the reduced report rate.** At 0.005 Hz (one report per 200 s), a
   single dropped report (realistic noise profile) already exceeds the 5-minute gap gate. At the default
   rate this ratio drops; not re-measured here.
2. **Two earlier attempts produced no estimates** and are not counted: (a) the registry topic was
   published before vehicles carried `current_pack_id`, so the worker loaded 0 packs and rejected the
   backlog as `unregistered_vin` (fixed by re-running `make seed`); (b) runs started at
   `START=2026-09-01` were behind the stream-processor's restored per-VIN watermark (30 Sep), so 22.4 M
   events were counted late and dropped (`stream_processor_counters_start.txt`): a sim must start after
   the last run's event time unless the processor state is reset (`make down`).
3. **Worker hang found and fixed during this run:** under local load the Kafka broker missed its own
   controller heartbeats, the group coordinator moved, and the worker's synchronous `commit()` blocked
   indefinitely (group empty, lag 228 K). The consumer now stores offsets per handled record and commits
   in the background (`enable.auto.offset.store=false`); after the restart it drained the backlog.
4. The simulator's HTTPS sender dropped 473,571 OEM-B records (~1.8 %) from its own client queue
   (`ErrQueueFull`) under laptop CPU contention; gateway back-pressure was not active. This is
   generator-side loss, not pipeline loss, and is not representative of the cluster benchmark.
