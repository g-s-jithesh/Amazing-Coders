# batch — CLAUDE.md

PySpark jobs over **Apache Iceberg on S3-compatible storage** (MinIO locally). They cover the brief's "batch analytics over historical data / billions of rows": the archive, rollups, nightly SoH reconciliation, ML feature tables, and reports (root §3.2, §5.4).

## Jobs

| Job | Type | Input → Output | Schedule |
|-----|------|----------------|----------|
| `archive_stream` | Structured Streaming | `telemetry.canonical.v1` → `raw.telemetry` | continuous |
| `rollups` | batch | `raw.telemetry` → `agg.telemetry_15m`, `agg.vehicle_daily` | hourly / daily |
| `sessions_history` | batch | `battery.sessions.v1` archive → `agg.charge_sessions` | hourly |
| `soh_nightly` | batch | sessions → reconciled SoH series per pack (same domain code as battery-intel) | nightly |
| `features` | batch | raw + agg → `features.soh_*`, `features.fault_risk_*` (via `libs/py-common/kilowatt/features`) | nightly |
| `reports` | batch | plans + sessions + tariffs → `report.charging_cost_monthly`, `report.carbon_monthly` | daily |
| `maintenance` | batch | expire snapshots, rewrite data files (compaction), remove orphan files | daily |
| `trip_segmentation` | batch | raw → `agg.trips` (DP / state machine; root §7) | daily |

## Table design

- `raw.telemetry`: partitioned by `days(ts_event)` and `bucket(64, vin)`; sorted by `(vin, ts_event_ms)`; Parquet + zstd. Target file size 256–512 MB after compaction.
- Every table has a documented schema in `batch/schemas/*.sql` (DDL), and schema evolution only adds columns.
- **Catalog:** Iceberg REST catalog or JDBC catalog on Postgres (cloud-agnostic; no Glue-only features). The endpoint, bucket and credentials come from env.
- Driver-linked data holds only the pseudonymous `driver_ref` (crypto-shredding makes it unlinkable after erasure; root §11).

## Rules

- **Exactly-once into Iceberg:** Structured Streaming checkpoints live in object storage (`s3a://…/checkpoints/<job>`), and each job has a stable query name. Don't delete checkpoints to "fix" a job without a runbook note.
- **Event time everywhere.** Late data: rollups recompute the affected partitions (idempotent `MERGE INTO` / overwrite by partition).
- **Never read `data/ground_truth/`.** Evaluation belongs to `ml/`.
- **Share code rather than copy it:** SoH math is imported from the battery-intel domain package (installed as a library) and features from `libs/py-common`. No reimplementation.
- **Cost-aware:** filter by partition; avoid `collect()`; broadcast small dimensions; no Python UDFs on the hot path when a built-in function exists.

## Local and cloud

- Local: `make batch JOB=<name>` runs spark-submit in the Spark container from compose against MinIO. Tests use a local SparkSession with small fixtures (tens of thousands of rows).
- Cloud: the same image and config via the Helm `CronJob` / Spark operator values. Only endpoints and credentials change (12-factor).
- Ad-hoc analysis: DuckDB with its Iceberg extension over the same tables, for the Solution Document's "billions of rows" demo. Record query times with `/evidence`.

## Test focus

- Schema contract per table.
- Rollup correctness against hand-computed fixtures.
- Idempotent re-run (run twice → same row counts and checksums).
- Late-data partition recompute.
- Trip segmentation on noisy GPS fixtures.
- Report totals equal the sum of plan costs (paise, exact).
- Maintenance job keeps the snapshots needed for time travel within the retention window.
