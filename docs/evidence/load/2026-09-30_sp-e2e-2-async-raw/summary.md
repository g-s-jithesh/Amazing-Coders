# Async raw writes (bounded queue), Scylla smp 2

**Verdict:** FAIL

**Headline:** demo alert 69.9 s; processor lag peaked at 1.04M; 5 batches never committed (stall); 1,500 oem_b records dropped by the simulator after 503s.

Hardware / setup: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB); 1 Kafka broker (RF 1), 1 gateway, Scylla 6.2 dev (smp 1 → 2), Redis 8, Mosquitto 2.0.20; simulator 100K vehicles @ 0.1 Hz, realistic noise, native transports, demo fault cooling_degradation with a 2 min precursor.

## Computed summary (`python summarise.py`)

```
published 2487936  https_dropped 1500
gateway accepted 2459194 rejected 2572 duplicates 30687  unaccounted -6017
processor events 2460679
demo alert DTC_RAISED P0A7E SEVERITY_CRITICAL: gateway-ingest -> processed 69720 ms, -> Kafka append 69852 ms
all alerts with a trigger (n=1): latency <= p50 inf s, p95 inf s, p99 inf s (bucket upper bounds)
sink kafka: 0 batch writes, mean 0 ms
sink redis: 0 batch writes, mean 0 ms
sink scylla: 0 batch writes, mean 0 ms
max lag processor 1044783
```

## Notes

Found two bugs: (a) commits stalled when input went idle (poll blocked forever) → poll timeout; (b) Kafka 'no usable partitions' produce failures during this run, whose sender retries the gateway could drop as duplicates → dedup-after-ack fix (run 7). 'Unaccounted' is negative (−6,017): records the client counted as dropped were in fact processed on retry.

## Caveats

Local laptop with every service sharing 12 logical CPUs; one broker, RF 1. Latency here is gateway receipt → alert
in Kafka (the vehicle → gateway hop is milliseconds locally; vehicle clocks carry simulated skew, so event time is
not used as the start). Histogram values are bucket upper bounds. The ≥ 100K events/s and p95 NFRs are measured on
the cloud cluster, not here.

## Reproduce

`OUT=<local dir> DURATION=180s VIN=<vin> make e2e` (tests/load/e2e-local.sh), then copy the folder here.
Runs 1–4 predate the harness and were captured with the same steps by hand; their `summarise.py` is the harness's.
