# dispatch-optimizer live plan round trip (F-08), 2026-10-01, local laptop

Create → idempotent repeat → approve → outbox relay → `dispatch.commands.v1`, against the compose stack
(`dispatch-optimizer`, `dispatch-relay`, Postgres with RLS, Kafka). Depot: the first depot of the seed
(`SEED=42`), 247 vehicles, 124 connectors, 1,809 kW site cap. Fleet source: `SeedFleet` (master data +
duty schedule, SoC fixed at 60 %; live SoC arrives with the fleet-api snapshot in Step 8).

| step | result |
|---|---|
| `POST /internal/v1/depots/{id}/dispatch-plans` (Idempotency-Key) | 201 in 3.67 s (solver budget 2 s + greedy safety net + baseline + 924 row inserts) |
| same request again | 200, same plan id |
| `POST …/approve` as dispatcher | 200, status APPROVED, outbox row written in the same transaction |
| relay | status PUBLISHED; topic record keyed by depot_id, 924 assignments, `approved_by` = user subject |
| money | `energy_cost_paise` (1,843,462) = Σ assignment `cost_paise` exactly |
| plan limits | peak 755.7 kW ≤ 1,809 kW cap; 0 missed departures (`create_summary.json`) |

## Honest notes

- **The single-plan "saving vs baseline" is not a result.** Over one horizon the baseline fills every
  pack to 100 % while the plan charges to each departure's requirement, so it buys less energy and
  dwells at lower SoC by construction. The fair comparison is the 30-day back-test with carried-over SoC
  (`../2026-10-01-backtest-5x30/`).
- **Relay bug found here and fixed:** PostgreSQL re-checks an UPDATEd row against the SELECT policy, and
  the relay's policy only admitted `APPROVED` rows, so `→ PUBLISHED` was refused and rolled back. The
  relay retried as designed, putting **10 records with one `plan_id`** on the topic before the fix (at-
  least-once; consumers dedupe by `plan_id`). Policy now admits `APPROVED|PUBLISHED` in USING.
- Plan creation is synchronous (budget-bounded). It is a heavy command, not a read: the API p95 target
  applies to reads; move creation to a job queue if depots outgrow the budget.
