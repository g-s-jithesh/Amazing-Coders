# Gateway dedup records keys only after Kafka ack (loss fix)

**Verdict:** PASS (no loss); CONCERN (dedup rate)

**Headline:** 0 unaccounted; processor and raw-sink both stored 1,893,897 events; duplicates caught fell to 307 (of ~19K injected).

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 1896122  https_dropped 0
gateway accepted 1893897 rejected 1918 duplicates 307  unaccounted 0
processor events 1893897  raw-sink events 1893897
all alerts with a trigger (n=1): latency <= p50 0.05 s, p95 0.05 s, p99 0.05 s (bucket upper bounds)
sink kafka: 1463 batch writes, mean 8 ms
sink redis: 1463 batch writes, mean 36 ms
sink scylla: 114 batch writes, mean 2413 ms
max lag processor 204975  raw-sink 1091038
```

## Notes

Correct but weak dedup: duplicates arrive within milliseconds of their original, before its produce completes → in-flight set (run 7).

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
