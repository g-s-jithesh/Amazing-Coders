# Stream processor, raw-store write on the alert path

**Verdict:** FAIL

**Headline:** demo P0A7E alert 23.3 s gateway→Kafka (target < 5 s); accounting exact (0 unaccounted).

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 2487936  https_dropped 0
gateway accepted 2460736 rejected 2567 duplicates 24633  unaccounted 0
processor events 2460736
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 21008 ms, -> Kafka append 23270 ms
all alerts with a trigger (n=1): latency <= p50 30.0 s, p95 30.0 s, p99 30.0 s (bucket upper bounds)
sink kafka: 0 batch writes, mean 0 ms
sink redis: 0 batch writes, mean 0 ms
sink scylla: 0 batch writes, mean 0 ms
```

## Notes

Raw Scylla writes ran before the Kafka emit in every batch, so every alert waited for the slowest sink. Led to the async-raw attempt (run 2).

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
