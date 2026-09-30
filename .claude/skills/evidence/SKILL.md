---
name: evidence
description: Run a benchmark or verification (load, soak, chaos, coverage, security scan, SQL plan, ML eval) and save the raw output plus hardware/config details under docs/evidence/ with a summary judged against the NFR targets. Use whenever a number will appear in docs, the README, the Solution Document or the demo.
argument-hint: "<load|soak|chaos|coverage|scan|sql|ml> [label]"
---

# Evidence run: $ARGUMENTS

Prime directive 4: **never fabricate numbers.** Every figure in the Solution Document must trace back to a folder this skill creates. A failed or disappointing run is still evidence: record it and don't hide it.

## 1. Parse the arguments

The arguments were: `$ARGUMENTS`

- First word = kind: `load | soak | chaos | coverage | scan | sql | ml`. If it's missing, ask.
- Second word (optional) = label (e.g. `local-10k`, `eks-3node`, `alerts-inbox`). Default: `run`.
- Output folder: `docs/evidence/<kind>/<YYYY-MM-DD>_<label>/`. If it already exists, append `-2`, `-3`, and so on. **Never overwrite** a previous run.

## 2. Capture the environment first

```bash
python3 "$CLAUDE_PROJECT_DIR/.claude/skills/evidence/capture_env.py" "docs/evidence/<kind>/<date>_<label>"
```

This writes `env.json` and `env.md`: git SHA and dirty flag, OS, CPU model and cores, RAM, Docker version and resources, and Kubernetes nodes if a cluster is reachable. Then **add the run configuration** to `env.md` by hand: vehicles, rate Hz, simulator mode, Kafka partitions and brokers, replica counts, the relevant service resource limits, and the dataset size.

## 3. Run it, capturing raw output with `tee`

**Write to a local, non-synced directory during the run** (e.g. `$TEMP/kw-evidence-<label>`), then copy the files into
`docs/evidence/...`. On 2026-09-30, writing into the repo while it sat in a OneDrive-synced folder produced a
reproducible 1–8 s Kafka-ack stall cluster that vanished when writing to temp (see
`docs/evidence/load/2026-09-30_gateway-e2e-local-3/`).

| Kind | Command (prefer Make targets; create them if missing) | Raw artefacts to keep |
|------|---------------------------------------------------------|-----------------------|
| load | `make load 2>&1 \| tee <dir>/raw.log` | load-driver JSON (eps sent/acked), k6 `--summary-export` JSON, consumer lag snapshots before/during/after (`kafka-consumer-groups.sh --describe --all-groups`), Prometheus query exports for e2e latency p50/p95/p99 |
| soak | `make soak DURATION=1h 2>&1 \| tee <dir>/raw.log` | same as load, sampled every 5 min; memory/CPU over time (`docker stats --no-stream` or `kubectl top`) |
| chaos | `make chaos 2>&1 \| tee <dir>/raw.log` | kill timestamp, recovery timestamp, events produced vs consumed (loss = 0?), lag curve, alert latency during the failure |
| coverage | `make test 2>&1 \| tee <dir>/raw.log` | per-service coverage XML/JSON/HTML, the Go `cover.out` files |
| scan | `make scan 2>&1 \| tee <dir>/raw.log` | Semgrep SARIF, Trivy JSON (fs + each image), gitleaks report, ZAP HTML/JSON |
| sql | run the query with `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` **before** the change → `before.txt`; apply the index/view/rewrite → `after.txt`; run each 5× and keep all timings | both plans, the DDL of the change, the row counts of the involved tables |
| ml | `make ml-eval 2>&1 \| tee <dir>/raw.log` | `ml/reports/*.json` copied in, the model artefact hash, the split definition |

For `load` against the NFRs:
- warm up for 2 min, then measure for ≥ 5 min
- for the burst test, hold 3× the baseline rate for 5 min
- **verify no loss:** events acknowledged by the gateway = rows in Scylla for the window = events consumed. Duplicates injected by the simulator don't count as loss.

## 4. Write `summary.md`, computed from the raw files

- Compute the metrics **with a script** (inline Python reading the JSON/log files), not by reading numbers off the log by eye. Save the script as `<dir>/summarise.py` so anyone can re-run it.
- Compare against the targets from root CLAUDE.md §2 and §21:

```
| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Ingest throughput | ≥ 100,000 eps | 38,412 eps | FAIL (local, 8-core laptop) |
| API p95 | < 200 ms | 143 ms | PASS |
```

- The measured values above are **format examples only**. Replace them with computed values.
- Put the hardware in one line directly under the table (e.g. "8 vCPU / 16 GB laptop, Docker Desktop, 1 broker").
- No rounding in our favour: use 3 significant figures, and floor for throughput, ceil for latency.
- Add a **Caveats** section (local vs cluster, reduced rate, single broker, warm cache…).
- Add **Reproduce:** the exact commands.

## 5. Index it and link it

- Append a row to `docs/evidence/README.md`: date, kind, label, headline result, verdict, and a link.
- If the result updates a number already quoted elsewhere (README, Solution Document drafts, CLAUDE.md §21 "Measured"), update that place and link to this folder.
- If a target was missed, say so plainly, suggest the most likely bottleneck from the metrics, and offer a follow-up. Don't silently re-run until it passes.

## 6. Report

Give the folder path, the verdict table, the caveats, and whether anything else in the docs was updated.
