# processor and raw-sink roles with separate consumer groups

**Verdict:** PASS (alert path); CONCERN (processor lag spike)

**Headline:** demo P0A7E FIRING alert 18 ms gateway→processed, 20 ms →Kafka; Kafka emit 10 ms, Redis 36 ms per batch; raw-sink lag peaked at 1.08M and drained to 0 after the run; processor lag peaked at 65,848 (~6.6 s of traffic) at simulator start-up on the shared laptop.

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 1896122  https_dropped 0
gateway accepted 1875371 rejected 1918 duplicates 18833  unaccounted 0
processor events 1875371  raw-sink events 0
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 1549 ms, -> Kafka append 1670 ms
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 18 ms, -> Kafka append 20 ms
all alerts with a trigger (n=1): latency <= p50 0.05 s, p95 0.05 s, p99 0.05 s (bucket upper bounds)
sink kafka: 1586 batch writes, mean 10 ms
sink redis: 1586 batch writes, mean 36 ms
sink scylla: 130 batch writes, mean 2357 ms
max lag processor 65848  raw-sink 1076947
```

## Notes

The RESOLVED line for the demo VIN is the previous run's episode, restored from vehicle.state.v1 across container restarts and resolved when the re-injected fault cleared the code (state restore working). The processor lag spike means an alert fired in that window would have exceeded 5 s; the p95 < 5 s NFR must be proven on the cluster load test.

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
