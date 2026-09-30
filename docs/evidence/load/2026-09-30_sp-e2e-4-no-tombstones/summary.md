# Frozen DTC list + UnsetValue (no tombstones)

**Verdict:** FAIL

**Headline:** demo alert 32.9 s; Scylla 2,847 ms per batch (2× faster, ~5.3K rows/s) but still below the 10K events/s input.

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 1896122  https_dropped 0
gateway accepted 1815605 rejected 1918 duplicates 78599  unaccounted 0
processor events 1815605
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 32805 ms, -> Kafka append 32888 ms
all alerts with a trigger (n=1): latency <= p50 inf s, p95 inf s, p99 inf s (bucket upper bounds)
sink kafka: 120 batch writes, mean 89 ms
sink redis: 120 batch writes, mean 567 ms
sink scylla: 120 batch writes, mean 2847 ms
max lag processor 510240
```

## Notes

Tombstone fix halved write cost, but a laptop dev Scylla still cannot keep up with the input, so any coupling of alerts to raw writes fails the NFR → role split (run 5).

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
