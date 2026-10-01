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
  main.py                 # internal API (plan → DRAFT, get, approve); composition root
  application/            # planning.py (stays, strategy + safety net), plans.py (lifecycle), backtest.py
  domain/
    schedule.py           # single-vehicle / batched DP, evaluate, charge-on-arrival baseline
    coordinate.py         # Lagrangian coordinator, repair, greedy fallback (Strategy pattern)
    degradation.py        # degradation-cost function (configured coefficients)
    tariff.py money.py    # IST slot prices; paise rounding
    connectors.py         # power profiles → connector assignments
    validate.py           # independent plan validator (the source of truth for invariants)
    plan.py               # plan lifecycle state machine
    routing/ assign/      # en-route A* + min-cost assignment (F-09, not yet built)
  infrastructure/         # db.py (plans, outbox), kafka.py (contract, producer), files.py (config, tariffs, seed fleet)
  workers/outbox_relay.py # dispatch.outbox → dispatch.commands.v1
config/degradation.toml, config/solver.toml
bench/
```

## Depot planner

- **Horizon:** 15-min slots; 36 h (T = 144) as built, see below. SoC buckets are 1 pp (S = 101). Power levels P = {0, …, min(charger max, vehicle max)}, discretised to ≤ 6 levels.
- **Transition:** `s' = s + η·P·Δt / usable_kwh(SoH)`, with η per charger type from config. Grid energy = `P·Δt` (you pay for grid-side kWh).
- **Cost per step:**
  - `tariff[t]·P·Δt`
  - `+ λ[site,t]·P` (the Lagrangian price for the site cap)
  - `+ degradation(s, P, pack_temp)`
- **Terminal:** `SoC(departure) ≥ required` is hard. If infeasible, return the best effort + an `INFEASIBLE` flag + the shortfall. **Never silently violate it.**
- Vectorise the DP over the SoC axis with numpy: O(T·S·P) per vehicle.
- **Coupling (site power cap):** subgradient updates of λ[site,t] until the cap holds at every slot, or the iteration or time budget runs out. Then repair with the greedy step (reduce power for the vehicle with the most slack).
- **v1 assumption (superseded, see As built):** one connector per parked vehicle.
- **Budget:** 2 s per depot (config). On timeout or error → greedy fallback (`method=GREEDY_FALLBACK`, flagged in the UI). A circuit breaker in callers.

### As built (Step 7, 2026-10-01)

- **Stays, not vehicles:** a vehicle's duty splits time into depot stays (return → next departure); each
  stay in progress now or starting in the next 24 h is one DP task. Horizon **144 slots (36 h)** so every
  such stay ends inside it; re-plan daily (rolling). A later stay is planned from `required − one shift's
  use` (conservative; the real arrival SoC is at least that).
- **Two coupling constraints per slot:** site kW cap (λ) **and connector count** (μ): the seed has ~0.5
  connectors per vehicle, so "one connector per vehicle" (the original v1 assumption) does not hold.
  Connectors are an AC-capable pool; vehicles may be re-plugged at slot boundaries; `domain/connectors.py`
  turns profiles into sticky, never-double-booked assignments. DC fast charging is left to en-route (F-09).
- **Lagrangian alone does not converge** on these integer instances (cohorts jump between slots;
  measured in `bench/bench_depot.py`): a deterministic ±0.1 % price dither, the least-violation iterate,
  `patience` early stop, then **batched repair rounds**. A **greedy safety net** is always computed too
  and kept if it misses fewer departures (the repair can leave a departure short; a test pins this case).
- **Charger taper is modelled in the DP** (SoC stops at the cap; only energy that fits is billed and
  stresses the pack), consistent with `evaluate` and the validator.
- **Config is TOML** (`config/*.toml`, stdlib `tomllib`), not YAML: no extra dependency.
- Tariff `DEPOT_TOD_SYNTH` (`data/reference/tariffs.csv`) is **synthetic**; its night window and levels
  follow the reported BESCOM EV ToD proposal; the evening peak is illustrative.
- **Plan creation is synchronous** within the budget (no job queue yet). Caller identity comes from
  `X-Tenant-Id`/`X-User-Id`/`X-Roles` until F-10; approve is dispatcher-only; cross-tenant → 404.
- **Fleet source:** `SeedFleet` (seed CSVs, SoC fixed at 60 %) until fleet-api serves a depot snapshot with
  live SoC (Step 8). **Outbox relay** is this service's own worker, connecting as `dispatch_relay`
  (outbox + APPROVED→PUBLISHED only; RLS policies per role).

## Degradation cost (pillar #1)

- A calendar and cycle stress function of SoC dwell, C-rate and temperature. The **coefficients come from `config/degradation.toml`** (literature-style defaults or estimated from our data), **never from the simulator's hidden parameters**; that would be leakage and would inflate results.
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
