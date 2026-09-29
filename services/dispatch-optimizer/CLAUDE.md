# dispatch-optimizer — CLAUDE.md

Python 3.12 / FastAPI + job workers. Answers **Q2: when and where each vehicle should charge at the lowest total cost, within depot power limits, without missing a departure** (root §1.2, §7). Innovation pillar #1 (**health-aware charging cost**) lives here.

## Owns

Postgres schema `dispatch`:
- `tariff`, `tariff_window`
- `dispatch_plan` (id, depot_id, version, status DRAFT/APPROVED/PUBLISHED/SUPERSEDED, method, horizon, created_by, cost_paise, baseline_cost_paise, degradation_cost_paise, solver_stats jsonb)
- `dispatch_assignment` (plan_id, vehicle_id, charger_id, slot_start, slot_end, power_kw)
- `charging_session` (planned vs actual)

Charger availability lives in Redis `charger:{id}:status`, from simulated OCPP-style status events.

Reads (via fleet-api API, not its schema): vehicles, depots, chargers, `vehicle_duty` (next departure, required SoC or planned km), and latest vehicle state.

## Layout

```
app/
  api/v1/internal/        # plan (async job → DRAFT), get plan, transition status, en-route recommendation
  application/            # PlanDepotCharging, RecommendEnRouteCharger, BacktestPlans
  domain/
    schedule/             # single-vehicle DP, Lagrangian coordinator, greedy fallback (Strategy pattern)
    degradation/          # degradation-cost function (configured coefficients)
    routing/              # graph (CSR arrays), A* with energy weights, geohash candidate search
    assign/               # min-cost assignment (scipy linear_sum_assignment)
    validate/             # independent plan validator (the source of truth for invariants)
    money.py              # paise arithmetic, rounding rules
  infrastructure/         # repositories, fleet-api client, redis, graph loader (data/graphs/*.npz)
  workers/plan_worker.py  # job queue consumer (Redis stream) with time budget
config/degradation.yaml, config/solver.yaml
bench/
```

## Depot planner

- **Horizon:** 24 h in 15-min slots (T = 96). SoC buckets are 1 pp (S = 101). Power levels P = {0, …, min(charger max, vehicle max)}, discretised to ≤ 6 levels.
- **Transition:** `s' = s + η·P·Δt / usable_kwh(SoH)`, with η per charger type from config. Grid energy = `P·Δt` (you pay for grid-side kWh).
- **Cost per step:**
  - `tariff[t]·P·Δt`
  - `+ λ[site,t]·P` (the Lagrangian price for the site cap)
  - `+ degradation(s, P, pack_temp)`
- **Terminal:** `SoC(departure) ≥ required` is hard. If infeasible, return the best effort + an `INFEASIBLE` flag + the shortfall. **Never silently violate it.**
- Vectorise the DP over the SoC axis with numpy: O(T·S·P) per vehicle.
- **Coupling (site power cap):** subgradient updates of λ[site,t] until the cap holds at every slot, or the iteration or time budget runs out. Then repair with the greedy step (reduce power for the vehicle with the most slack).
- **v1 assumption:** one connector per parked vehicle (document it). v2: connector sharing via time-indexed assignment.
- **Budget:** 2 s per depot (config). On timeout or error → greedy fallback (`method=GREEDY_FALLBACK`, flagged in the UI). A circuit breaker in callers.

## Degradation cost (pillar #1)

- A calendar and cycle stress function of SoC dwell, C-rate and temperature. The **coefficients come from `config/degradation.yaml`** (literature-style defaults or estimated from our data), **never from the simulator's hidden parameters**; that would be leakage and would inflate results.
- Price it as `₹ per pp SoH lost × pack replacement cost / usable SoH range`. Make it configurable and show it in the plan breakdown.

## En-route recommendation

1. Candidates: geohash-6 k-ring around the vehicle, expanding until ≥ K chargers or the max radius.
2. **A\*** on the road graph (preprocessed OSM extract in CSR `.npz`; ODbL, declared in `docs/declarations.md`). Edge weight = kWh(distance, speed class, gradient if available). Heuristic = haversine × min kWh/km (admissible). A charger is reachable if the energy is ≤ usable minus the reserve.
3. When several requests arrive within a micro-batch window (e.g. 10 s): min-cost assignment with cost = travel time + expected wait + energy price. One column per free connector.
4. No reachable charger → return `NO_REACHABLE_CHARGER` + the nearest option + the energy deficit. The API returns 200 with that result (it's a business outcome) and an alert is raised. Never a 500.

## Baseline and back-test

- **Baseline:** charge-on-arrival at max power, the same validator and the same cost function.
- `BacktestPlans` replays N simulated days → savings %, peak kW vs cap, missed departures. Results go through `/evidence ml` or `/evidence load`. Never hand-typed.

## Money and units

Tariffs are integer paise/kWh. Internal numpy computation uses float64. Convert to paise with half-even rounding **once**, at plan output. Tests assert that the plan total = the sum of assignment costs exactly.

## Test focus

- **Property tests on `validate/`:**
  - SoC stays within [floor, cap]
  - departure SoC ≥ required when feasible
  - the site cap holds at every slot
  - no connector is double-booked
  - cost ≤ baseline on random depots
- DP against brute force on tiny instances.
- A* against Dijkstra on random graphs.
- An infeasible case is reported.
- A tariff window across midnight.
- IST vs UTC slot alignment.
- Timeout → greedy fallback.
- Money rounding.
- Benchmark: 1,000-vehicle depot solve time (record with `/evidence`).
