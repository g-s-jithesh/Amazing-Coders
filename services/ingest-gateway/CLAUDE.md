# ingest-gateway — CLAUDE.md

Go service. It is the **front door for all telemetry**: it authenticates, decodes, validates and normalises every OEM payload into the canonical Protobuf and publishes it to Kafka. It must sustain 100K+ eps and a 3× burst **without silent data loss** (root §2, §3, §4).

## Contracts

**In**
- MQTT shared subscription `$share/ingest/v1/+/+/#` (QoS 1). Topics: `v1/oem_a/{vin}/telemetry`, `v1/oem_c/{vin}/t`.
- HTTPS `POST /ingest/v1/{oem}/batch` (mTLS client cert **or** OEM API key + HMAC-SHA256 body signature with a timestamp header; reject if skew > 5 min). Max body 256 KB; max 500 events per batch.
- `GET /healthz`, `GET /readyz` (ready = Kafka producer healthy and not in back-pressure), and `/metrics`.

**Out (Kafka, key = VIN)**
- `oem.raw.<oem>.v1`: raw bytes for every *authenticated* message (enables replay after an adapter fix).
- `telemetry.canonical.v1`: validated canonical `TelemetryEvent`.
- `telemetry.dlq.v1`: original bytes + headers `x-dlq-reason`, `x-oem`, `x-error`, `x-received-ms`.

**DLQ reasons (enum; keep the simulator's malformed types aligned):** `AUTH`, `IDENTITY_MISMATCH`, `OVERSIZE`, `UNKNOWN_OEM`, `UNKNOWN_SCHEMA_VERSION`, `DECODE`, `SCHEMA`, `VIN_FORMAT`, `VIN_CHECKSUM`, `DTC_FORMAT`, `RANGE`.

## Layout

```
cmd/ingest-gateway/main.go
internal/domain/
  vin/ dtc/            # pure parsers/validators (property + fuzz tested)
  canonical/           # canonical event value object + range rules (soc 0–100, temp −40..90 °C, etc.)
  pipeline/            # Chain of Responsibility: Step interface + ordered steps
  dedup/               # rotating Bloom filter (pure) + Confirmer port
internal/app/          # IngestService: runs pipeline, routes to Publisher port, back-pressure policy
internal/adapters/
  oem/oem_a oem_b oem_c/  # Adapter pattern: Decode([]byte, meta) → canonical.Event | error
  oem/registry.go         # Factory: selects adapter by topic prefix / header
  mqtt/ http/ kafka/ redis/
```

## Pipeline order (don't reorder without an ADR)

1. authenticate / identity check
2. size limit
3. **publish raw**
4. select adapter
5. decode
6. schema validate
7. VIN (format + check digit)
8. DTC parse
9. range checks
10. dedup
11. enrich (`event_id` UUIDv7, `ts_ingest_ms`)
12. publish canonical

Any failure → DLQ with its reason, then continue with the next message.

## Identity rules (anti-spoofing, STRIDE "S")

- Per-vehicle cert: `CN == VIN` in the topic **and** in the payload, otherwise `IDENTITY_MISMATCH`.
- OEM-cloud cert or key: `CN == oem id` **and** that OEM is registered for the VIN (the `vin → oem` map is loaded from Postgres at startup and refreshed every 5 min; it's a cache, so on a miss, allow it and flag it with a metric, since the stream-processor also checks).

## Dedup (root §3.4)

- A rotating pair of Bloom filters over `(vin, seq)` with a 10-min window and 1% FPR, sized from the configured eps.
- A Bloom hit → Redis `SET dedup:{vin}:{seq} 1 NX EX 600`. Only drop the event when Redis confirms it has been seen.
- **Redis down → skip dedup** (downstream sinks are idempotent), increment `dedup_degraded_total`, and don't fail ingestion. This is graceful degradation. Test it.

## Back-pressure (never drop accepted data)

- A bounded channel sits between the pipeline and the Kafka producer. When the producer buffer passes the high watermark:
  - MQTT: stop acking QoS 1 (manual ack), so the broker holds and redelivers.
  - HTTPS: return `503` + `Retry-After`. `/readyz` goes false, so the LB sheds load.
- Kafka producer (franz-go): `acks=all`, idempotent, `linger 5ms`, zstd, batch ≤ 1 MB, `key = VIN`. Ack MQTT or return 2xx **only after the produce callback succeeds**.

## Adding an OEM (no downtime)

1. Add `internal/adapters/oem/<oem>/` implementing `Adapter`.
2. Add golden samples in `libs/oem-samples/<oem>/`.
3. Register it (topic prefix / header) in the registry.
4. Write contract tests against the samples.
5. Deploy as a rolling update. Messages for an unregistered OEM go to the DLQ `UNKNOWN_OEM` and can be replayed from `oem.raw.*` after release.

## Observability

- Metrics: `ingest_events_total{oem,result}`, `dlq_total{reason}`, `dedup_hits_total`, `dedup_degraded_total`, `produce_latency_seconds`, `backpressure_active`, `mqtt_unacked`.
- Start an OTel span per message and inject the trace context into the Kafka headers.
- **Never log payloads or coordinates.** Log the VIN (hashed in non-debug), the reason, and the trace_id.

## Test focus

- Fuzz the VIN, DTC and each adapter (must never panic).
- Golden-sample round trips.
- Every DLQ reason.
- Bloom false-positive path (Redis says new → keep the event).
- Redis down.
- Kafka down → no ack / 503, then recovery with zero loss (Testcontainers: stop and restart the Kafka container).
- Identity mismatch.
- HMAC replay (old timestamp).
- Benchmark: events/s per core through the full pipeline with a mocked publisher.
