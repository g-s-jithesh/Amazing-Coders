# In-flight dedup set on top of dedup-after-ack

**Verdict:** PASS

**Headline:** 0 unaccounted of 1,896,122; duplicates caught 18,833 (≈1 %, as injected); processor and raw-sink both processed/stored 1,875,371; Kafka emit 7 ms, Redis 32 ms, Scylla 1,311 ms per batch; both lags drained to 0.

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 1896122  https_dropped 0
gateway accepted 1875371 rejected 1918 duplicates 18833  unaccounted 0
processor events 1875371  raw-sink events 1875371
all alerts with a trigger (n=1): latency <= p50 0.1 s, p95 0.1 s, p99 0.1 s (bucket upper bounds)
sink kafka: 1959 batch writes, mean 7 ms
sink redis: 1958 batch writes, mean 32 ms
sink scylla: 193 batch writes, mean 1311 ms
max lag processor 71936  raw-sink 422823
```

## Notes

The demo VIN (reused from run 2) still had its P0A7E episode restored as firing, so correctly no new FIRING alert was emitted; run 5 is the demo-latency record. Processor lag peaked at 71,936 at start-up (same caveat as run 5).

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
