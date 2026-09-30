# Poll-timeout fix + per-sink timing + 5 min state snapshots

**Verdict:** FAIL

**Headline:** demo alert 50.6 s; Scylla 5,574 ms per ~14K-row batch (~2.6K rows/s); Kafka emit 47 ms.

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 1882724  https_dropped 2621
gateway accepted 1860081 rejected 1903 duplicates 18674  unaccounted -555
processor events 1861066
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 50489 ms, -> Kafka append 50616 ms
all alerts with a trigger (n=1): latency <= p50 inf s, p95 inf s, p99 inf s (bucket upper bounds)
sink kafka: 130 batch writes, mean 47 ms
sink redis: 129 batch writes, mean 360 ms
sink scylla: 130 batch writes, mean 5574 ms
max lag processor 691015
```

## Notes

Per-sink timings located the bottleneck: the raw store, not processing or Kafka. Kafka idle CPU fell from ~250 % to ~2 % after the snapshot cadence change.

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
