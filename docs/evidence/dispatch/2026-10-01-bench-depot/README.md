# Depot solve benchmark (F-08), 2026-10-01, local laptop

`uv run python -m bench.bench_depot 50,250,1000 2,5,15,40` → `bench_depot.jsonl` (one line per run).

- Instances: **synthetic random depots** (`bench/bench_depot.py`, seed = size), 24 h (T = 96), four vehicle
  models, arrivals 17:30–23:30 IST, site cap = 0.35 × Σ vehicle max kW, connectors = vehicles / 2,
  tariff 22:00–06:00 ₹4.50 else ₹8.50/kWh (bench-only prices), degradation coefficients as in tests.
- Measures `plan_site` alone (no greedy safety net, no baseline, no DB) at several Lagrangian iteration
  caps (`max_iter`; `patience` = 3 stops earlier when the violation stops improving).
- Hardware: Windows 11 laptop, Intel 12-thread CPU (`Intel64 Family 6 Model 154`), Python 3.12.11.

| vehicles | solve time (s) across max_iter 2–40 | hard violations | missed departures (plan / baseline / greedy) | cost vs baseline | cost vs greedy |
|---|---|---|---|---|---|
| 50 | 0.18–0.25 | 0 | 0 / 0 / 0 | −44.5 % | −26.8 % |
| 250 | 0.26–0.44 | 0 | 0 / 0 / 0 | −44.7 to −45.6 % | −25.6 to −26.8 % |
| 1,000 | 1.24–2.41 | 0 | 0 / 0 / 0 | −44.4 to −45.2 % | −26.3 to −27.4 % |

Reading: beyond 5 iterations the plan does not change (cost identical), which is why `solver.toml` uses
`max_iter = 8, patience = 3`. At 1,000 vehicles the solve brushes the 2 s budget on this laptop, so a
depot that large would sometimes get the greedy fallback here. **These are random synthetic depots with a
bench tariff; the savings are not the headline result** (see the seed-depot back-test).
