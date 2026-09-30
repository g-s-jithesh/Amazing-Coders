# Simulator generation throughput — kafka-direct, local (2026-09-30)

**What this measures:** how fast `services/simulator` can generate, noise, encode and hand 100K vehicles' telemetry
to Kafka (`oem.raw.*`, raw OEM payloads) with no gateway and no consumers. It is the simulator's own target
(`services/simulator/CLAUDE.md` → Performance), **not** the platform's end-to-end ingest NFR (≥ 100K eps through
gateway → processor), which is measured later on the cluster.

| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Simulator generation throughput (steady state, incl. broker stalls) | ≥ 100,000 eps on one node | 152,628 eps | PASS (local, 12 logical CPUs, not 8 vCPU) |
| Peak 10 s window | — | 355,324 eps | info |
| Loss: simulator published vs broker offset delta | 0 | 18,142,411 vs 18,142,411 → 0 | PASS |
| Publish errors | 0 | 0 | PASS |

Hardware: Intel i5-12450H laptop, 12 logical CPUs / 15.7 GB, Windows 11, Docker Desktop (13.8 GB), 1 Kafka broker (KRaft, RF 1, 6 partitions per `oem.raw.*` topic).

Run configuration: 100,000 vehicles · `--rate-hz 1` · `--speedup 0` (as fast as possible) · `--noise realistic` ·
180 s of sim time (18,000,000 clean samples → 18,142,411 published incl. ~1 % duplicates) · `--workers 12` ·
producer: idempotent, acks=all, zstd, linger 5 ms, 1 MB batches, 200K buffered records · code at `652609a`
(tree dirty only because of the evidence folders themselves).

## Findings

- **The bottleneck was the single local broker, not the simulator.** Throughput alternated between ~350K eps
  bursts and full stalls (one 10 s window published 0) when the producer's 200K-record buffer filled and `Produce`
  blocked — back-pressure working as designed, with zero loss.
- The earlier run (`../2026-09-30_sim-kafka-direct-local/`, empty broker) held ~336K eps without stalls, but its
  loss check was not captured, so its number is **not** reported.
- Both runs published exactly 18,142,411 events: the run is deterministic for a given seed.

## Caveats

- Local laptop, one broker on Docker Desktop's overlay filesystem after ~18M earlier records; a real cluster
  (3 brokers, 48 partitions, NVMe) should not show these stalls.
- `kafka-direct` skips the gateway; it is a generator/broker test only.
- 180 s run: shorter than the 5 min load protocol, acceptable for a generator smoke benchmark, not for NFR claims.

## Reproduce

```bash
make up
MSYS_NO_PATHCONV=1 docker exec kilowatt-kafka-1 /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic 'oem\.raw\..*' --time -1 > offsets_before.txt
make sim MODE=kafka-direct RATE_HZ=1 SPEEDUP=0 DURATION=180s 2>&1 | tee raw.log
MSYS_NO_PATHCONV=1 docker exec kilowatt-kafka-1 /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic 'oem\.raw\..*' --time -1 > offsets_after.txt
python summarise.py
```
