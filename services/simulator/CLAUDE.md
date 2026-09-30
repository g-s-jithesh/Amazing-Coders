# simulator — CLAUDE.md

Go service. It generates the synthetic 100K+ EV fleet. **It's a graded deliverable**: the brief says "the simulator itself is part of your solution". Root rules still apply (see `/CLAUDE.md` §6). This file adds what matters inside this service.

## Responsibilities

- Produce the master data seed: tenants, fleets, depots, vehicle models, packs, vehicles, drivers (synthetic names only), charger sites, and duty schedules. Output goes to Postgres through `make seed`.
- Stream telemetry in the 3 OEM formats (`oem_a` MQTT JSON, `oem_b` HTTPS batch JSON with imperial units, `oem_c` compact binary/CSV-like with hex DTCs).
- Model physics (energy, SoC, voltage, temperature), **battery ageing**, **fault precursors**, and **noise**.
- Write **ground truth** (true SoH per pack per day, injected faults with precursor start and DTC time) to `data/ground_truth/`. This is the only service allowed to write there. Nothing in the pipeline reads it.

## Layout

```
cmd/simulator/main.go            # flags, wiring, graceful shutdown
internal/domain/
  vin/        # synthetic VIN generator (WMIs from data/reference/wmi_synthetic.csv) + check digit
  vehicle/    # vehicle state machine: parked → driving → returning → charging
  energy/     # kWh/km model: speed, ambient (HVAC load), payload; regen
  battery/    # OCV(SoC) curve, V = OCV − I·R, thermal model, cell spread, AGEING MODEL
  fault/      # fault catalogue, precursor curves, DTC emission
  route/      # city road points / depot loops (Bengaluru, Chennai, Surat bounding boxes)
  noise/      # duplicates, reordering, jitter, dropouts, clock skew, bursts, malformed payloads
internal/app/                    # tick scheduler, sharding, sim clock, backfill orchestration
internal/adapters/
  mqtt/ https/ kafka/            # transports (kafka = load-test only)
  encoders/oem_a|oem_b|oem_c/    # canonical in-memory state → OEM wire format
  groundtruth/                   # parquet writer (data/ground_truth/)
  seed/                          # CSV/COPY writer for master data
bench/
```

## CLI contract (keep the README and Makefile in sync)

`--seed` · `--vehicles` (default 100000) · `--tenants` · `--rate-hz` (per vehicle) · `--mode mqtt|https|kafka-direct|backfill` · `--shard i/n` · `--speedup` (sim-seconds per wall-second) · `--start` (sim start time, UTC) · `--days` (for backfill) · `--noise clean|realistic|hostile` · `--fault-rate` · `--ground-truth-out` · `--demo-inject <vin>:<fault>` (triggers a fault on demand for the demo video).

- `backfill` writes months of history at reduced resolution (e.g. 1 event/min) straight into the raw Iceberg layout, so ML and batch have history without waiting for real time. **Document this honestly** as synthetic backfill.

## Invariants (test all of them)

1. **Determinism.** Each vehicle's RNG is seeded from `hash(seed, vin)`, so output doesn't depend on shard count or goroutine scheduling. Test: same seed → same SHA-256 over the first N events per vehicle, for shards=1 and shards=4.
2. **Physics sanity.** SoC stays in [0, 100]; energy is conserved within tolerance over a trip; temperature relaxes toward ambient when idle; `pack_current_a` > 0 while driving and < 0 while charging (the canonical sign convention).
3. **Ageing.** SoH is monotonically non-increasing (except for measurement noise, which isn't added to the ground truth). Calendar fade ∝ √t; faster with high temperature (Arrhenius) and high-SoC dwell. Cycle fade ∝ EFC, faster with DoD, C-rate and temperature. IR grows as capacity fades.
4. **Faults have precursors.** Each injected fault has a precursor that starts ≥ 24–120 h before its DTC (configurable per fault type) and that is visible in the telemetry signals listed in root §6. Ground truth records `precursor_start_ts` and `dtc_ts`.
5. **Noise rates** fall within ±10% of the configured values over 1M events. Malformed payloads are exactly the configured types, so the gateway's DLQ reasons can be checked against them.
6. **VINs.** 17 characters, no I/O/Q, valid check digit, synthetic WMIs only. The malformed-VIN noise deliberately breaks the check digit.

## Demo fault injection

Use a precursor of **≥ 12 h sim time** (compress it with `--speedup`). The pack's thermal time constant is ~3 h, so a
2 h cooling-degradation precursor raises P0A7E while the pack is only ~9 °C above ambient, which is not believable on
camera. Measured at DTC time (Bengaluru, Sept): 2 h → +9 °C, 6 h → +14 °C, 12 h → +17 °C, 48 h → +25 °C.

## Identity model

- A **demo subset** (e.g. 1,000 vehicles) connects over MQTT with **per-vehicle mTLS certs** (CN = VIN), issued by `infra/pki` scripts at startup into a tmpfs.
- **Bulk traffic** simulates the device-free OEM-cloud model: one connection pool per OEM with an OEM cert (CN = `oem_a` …), and the VIN in the topic or payload. The gateway checks that the OEM may publish for that VIN.

## Performance

- **Target:** ≥ 100K eps from one 8-vCPU node in `kafka-direct` mode, and the same through the gateway on the cluster. Measure with `/evidence load`.
- Keep the per-tick path allocation-free: preallocate vehicle structs, pool encoders' buffers, batch publishes (MQTT pipelining; Kafka batches of 1–5K).
- Vehicles are processed in slices per goroutine (not one goroutine per vehicle).

## Golden samples (shared contract)

Each encoder writes canonical fixtures to `libs/oem-samples/<oem>/` (valid, plus one file per malformed type). The gateway's adapter tests parse the same files. **If you change an encoder, regenerate the samples and run the gateway tests too.**

## Test focus

Determinism across shards · ageing curve shape (table-driven against the expected formula) · precursor-before-DTC ordering · noise-rate tolerances · encoder golden files · VIN check digit · backfill partition layout · a benchmark for events/s per core.
