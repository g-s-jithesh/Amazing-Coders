# Solution Document (draft, mirrors the organiser template)

Sections point to the source of truth; numbers live in `docs/evidence/` and nowhere else.

1. **Executive summary.** Project Kilowatt: EV fleet battery health, health-aware charging dispatch and fault diagnostics on a
   simulated 100 K-vehicle fleet. Built: simulator, multi-OEM ingest, real-time rules and alerts, SoH estimation, depot
   charging optimiser with approval workflow, ML models for SoH and 7-day fault risk, a one-page console. Not built: see `docs/risks.md`.
2. **Problem and validation.** Fleet operations managers charge ad hoc, find battery faults after breakdowns and receive raw
   DTCs. Source for the tariff shape: BESCOM EV time-of-day proposal (reported by WRI India / Deccan Herald; synthetic here).
3. **Solution, value, innovation.** (1) Health-aware charging cost (energy + degradation in the objective, F-16);
   (2) fault precursors made learnable by the simulator (F-15); (3) approval-gated dispatch with a transactional outbox.
4. **Features.** `docs/feature-matrix.md`.
5. **High-level design.** `docs/architecture/c4-containers.md`; ADR-0001..0006 in `docs/adr/`; data stores per ADR-0002.
6. **Low-level design.** Layering per service `CLAUDE.md`; algorithms: VIN/DTC parsing, Bloom + Redis dedup, event-time windows and
   EWMA rules (stream-processor), coulomb counting + local-linear-trend Kalman + sqrt(t) RUL (battery-intel), batched DP over
   (slot, SoC) + Lagrangian coupling + repair + greedy safety net (dispatch-optimizer). Patterns in code: Adapter, Strategy,
   Repository, Transactional Outbox, Chain of Responsibility (gateway), Factory (adapter registry).
7. **NFRs and benchmarks.** `docs/evidence/load/` (gateway and stream-processor, local, caveats stated), `docs/evidence/dispatch/`
   (solve time vs depot size). The 100 K events/s and p95 targets are **not** demonstrated.
8. **Security.** RLS per service (non-superuser roles, separate relay role, tested), 404-not-403, input validation,
   problem+json errors. Not built: OIDC/JWT, mTLS, vault.
9. **Test strategy.** Unit + property tests (hypothesis), integration tests against the running stack (`-m integration`),
   leakage tests in `ml/`, GitHub Actions: Go (race, coverage), buf lint, Python (ruff, mypy --strict, coverage gate), image builds.
10. **Observability.** Prometheus metrics on every service; dashboards not built.
11. **AI/ML.** `docs/evidence/ml/2026-10-01-colab/` (protocol, baselines, bootstrap CIs); trained on Google Colab (`ml/train_colab.ipynb`). SoH MAE 0.40 pp (raw coulomb count 0.29, linear-by-age 2.40); fault risk PR-AUC 0.57 vs 0.36 for threshold rules, recall 0.43 at precision 0.5 and 39 h median lead time (targets not met).
12. **ADRs, risks, future work.** `docs/adr/`, `docs/risks.md`.
13. **Demo.** `docs/demo-script.md`.
14. **Repository checklist.** README, Makefile, `.env.example`, CI, evidence folders; tag `v1.0-submission`.
16. **Declarations.** `docs/declarations.md` (AI assistance, open-source components and licences, data statement).
