# Console alert path demo, 2026-10-01, local laptop (Docker capped to 5 GB)

`make sim RATE_HZ=0.01 SPEEDUP=30 DT=10 START=2026-10-01T20:00:00Z DURATION=25m INJECT=<vin>:insulation_wear PRECURSOR=10m`
(100K vehicles, realistic noise) with the stack running; the injected VIN's HV-isolation precursor raised DTC `P0AA6`.

- `alerts_recent.json`: what the console's alerts panel reads (`GET /internal/v1/alerts/recent?min_severity=HIGH`, tenant A):
  CRITICAL `DTC_RAISED P0AA6` FIRING, **gateway receipt to alert emit 278 ms** (the earlier P0A7E episode of the same VIN shows
  RESOLVED, 994 ms). Single sample each; not a distribution (see `docs/evidence/load/` for runs with histograms).
- `alerts_recent_other_tenant.json`: tenant B sees none of it (`{"alerts":[]}`).

Notes: three earlier attempts failed for harness reasons, listed because they cost time: (1) Mosquitto was OOM-killed by a 64 MB
container limit I had set (exit 137), (2) the vehicle registry topic was empty after a Docker restart, so the first alert had no
tenant (fixed by `make seed`), (3) the simulator restarts per-vehicle sequence numbers every run, so a second run within the
gateway's 10-minute dedup window is dropped as duplicates (`redis-cli flushall` between runs).
