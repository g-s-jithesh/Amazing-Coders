# Depot planner back-test (F-08/F-16): 5 seed depots x 30 days, 2026-10-01, local laptop

`make dispatch-backtest DEPOTS=5 DAYS=30` -> `result.json` (raw, includes hardware). Seed `SEED=42`; first 5 depots
(about 247 vehicles, 124 connectors, ~1.8 MW cap each). Daily rolling replan, executed against actual SoC; day 0 is
warm-up. Baseline = charge on arrival at max AC power, first come first served over the same connectors, same
cost function. Tariff `DEPOT_TOD_SYNTH` (synthetic), degradation coefficients are assumptions
(`config/degradation.toml`); SoH fixed at 100 %.

| | optimised | baseline |
|---|---|---|
| energy cost (paise) | 384,735,840 | 431,055,486 |
| degradation cost (paise) | 565,356,210 | 577,310,185 |
| total cost (paise) | 950,092,050 | 1,008,365,671 |
| departures / missed | 37,050 / 0 | 37,050 / 0 |

**Saving: energy cost -10.75 %, energy + degradation -5.78 %** (target in CLAUDE.md was >= 15 %: not met on
this tariff). Plan solve time median 0.9-1.5 s per depot-day, method LAGRANGIAN_REPAIRED in 150/150 plans.

## Honest notes
- The **site cap never binds** in these depots (peak 0.95-1.1 MW vs 1.8 MW cap); the connector count is the
  binding constraint. "Peak <= cap" holds for both sides, so it is not a differentiator here.
- The optimised peak is *higher* than the baseline's (load is concentrated in the cheap night window).
- The degradation figures depend entirely on assumed coefficients; treat that part as illustrative.
- The tariff's spread (4.85 vs 3.85 INR/kWh in the source proposal) is narrow; a wider TOU spread would
  raise the energy saving. We did not tune the tariff to hit the target.
