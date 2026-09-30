# Gateway end-to-end accounting — local, run outside OneDrive (2026-09-30)

Identical procedure to `-2`, but every file was written to a local temp directory during the run and copied here
afterwards.

| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Records unaccounted (published − accepted − rejected − duplicates) | 0 | 0 of 1,240,811 | PASS |
| `telemetry.canonical.v1` delta = accepted | equal | 1,227,193 = 1,227,193 | PASS |
| `telemetry.dlq.v1` delta = rejected | equal | 1,260 = 1,260 | PASS |
| DLQ reasons = injected malformed kinds only | 5 kinds | DECODE 244 · DTC_FORMAT 249 · RANGE 277 · UNKNOWN_SCHEMA_VERSION 244 · VIN_CHECKSUM 246 | PASS |
| Duplicates removed vs ~1 % injected | ≈ 1 % | 0.996 % | PASS |
| Produce latency per message (gateway → all Kafka acks) p50 / p95 / p99 | — | ≤ 32 ms / ≤ 128 ms / ≤ 512 ms | PASS (no stall cluster) |
| Simulator oem_b HTTPS records dropped | 0 | 0 | PASS |

Hardware: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB), 1 Kafka broker
(RF 1), 1 gateway container, Mosquitto 2.0.20, Redis 8. Code at `7f0efd8`.

## Finding

The 1–8 s produce-latency cluster seen in `-1` and `-2` (2 of 2 runs writing into the OneDrive-synced repo) did not
appear here, nor in 4 probe runs writing to temp (0 of 4). Most likely cause: OneDrive syncing files in the repo
during the run stalls host disk I/O and with it Docker Desktop. Not a gateway defect. n is small (2 vs 4 runs), so
treat it as a strong indication, not a proof; the cluster load test (F-13 observability, time-series latency) will
settle it. Evidence runs now write to a local temp directory first (`.claude/skills/evidence/SKILL.md`).

## Caveats

Local laptop, one broker, RF 1; this is functional accounting at ~10K eps, not the 100K eps NFR. Latency is
gateway → Kafka ack, not event → dashboard. Histogram buckets are powers of two, so values are upper bounds.

## Reproduce

```bash
make up
MSYS_NO_PATHCONV=1 docker exec kilowatt-kafka-1 /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic 'oem\.raw\..*|telemetry\.canonical\.v1|telemetry\.dlq\.v1' --time -1 > offsets_before.txt
curl -s localhost:8081/metrics > metrics_before.txt
make sim DURATION=120s > sim.log 2>&1
# wait until mqtt_unacked is 0 in /metrics, then:
curl -s localhost:8081/metrics > metrics_after.txt
MSYS_NO_PATHCONV=1 docker exec kilowatt-kafka-1 /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic 'oem\.raw\..*|telemetry\.canonical\.v1|telemetry\.dlq\.v1' --time -1 > offsets_after.txt
python summarise.py
```
(Run it in a local, non-synced directory and copy the files here afterwards.)
