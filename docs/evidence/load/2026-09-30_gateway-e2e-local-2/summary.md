# Gateway end-to-end accounting — local, fresh broker (2026-09-30)

Same run as `../2026-09-30_gateway-e2e-local/`, after `make down && make up && make seed`, to test whether the
latency stall cluster came from the aged broker.

| Metric | Target | Measured | Verdict |
|--------|--------|----------|---------|
| Records unaccounted | 0 | 0 of 1,240,811 (1,227,193 accepted + 1,260 rejected + 12,358 duplicates) | PASS |
| Canonical / DLQ offset deltas = accepted / rejected | equal | 1,227,193 / 1,260 | PASS |
| Produce latency p50 / p95 / p99 | — | ≤ 64 ms / ≤ 256 ms / ≤ 4.1 s | CONCERN (same as before) |

**Result: aged-broker hypothesis refuted** — the stall cluster is identical on a fresh broker. Follow-up probes ruled
out a cold gateway, a cold producer, run length and Kubernetes contention (0 slow produces in 310 s of probe traffic).
The only remaining difference was that evidence runs write their files into the repo, which lives in a
**OneDrive-synced folder**. See `../2026-09-30_gateway-e2e-local-3/`.

Hardware and reproduce: as in `../2026-09-30_gateway-e2e-local-3/summary.md`.
