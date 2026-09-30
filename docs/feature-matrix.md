# Feature Matrix

Source for Solution Document §4. Every feature is traceable to code, tests and a demo timestamp.
Update with `/feature-done F-xx`. Status: Done / Partial / Planned. Priority: MoSCoW.

| ID | Feature | User story | Priority | Status | Code path | Tests | Video | Last verified |
|----|---------|------------|----------|--------|-----------|-------|-------|---------------|
| F-01 | 100K-EV simulator with ageing, faults, noise | As an evaluator I can reproduce realistic fleet data from a seed | Must | Partial (3a master data; 3b physics + ageing) | services/simulator/ | services/simulator/internal/**/*_test.go | | 2026-09-30 |
| F-02 | Multi-OEM ingestion & normalisation | As a platform team I can onboard an OEM format without downtime | Must | Planned | services/ingest-gateway/ | | | |
| F-03 | Schema validation, VIN/DTC parsing, dedup, DLQ | As ops I trust bad data never corrupts analytics | Must | Planned | services/ingest-gateway/ | | | |
| F-04 | Live fleet map (< 2 s) | As a dispatcher I see every vehicle's SoC and location live | Must | Planned | services/stream-processor/, services/fleet-api/, web/ | | | |
| F-05 | Real-time battery safety alerts (< 5 s) | As a technician I'm alerted to thermal / isolation / interlock faults | Must | Planned | services/stream-processor/ | | | |
| F-06 | DTC decoding + runbook diagnostics (RAG) | As a technician I know what a code means and what to do | Must | Planned | services/battery-intel/ | | | |
| F-07 | SoH estimation (coulomb counting + Kalman) | As a fleet manager I see each pack's health with a confidence interval | Must | Planned | services/battery-intel/ | | | |
| F-08 | Depot charging optimiser (DP + TOU + site cap) | As a dispatcher I get the cheapest plan that meets every departure | Must | Planned | services/dispatch-optimizer/ | | | |
| F-09 | En-route charger dispatch (A* + assignment) | As a driver low on charge I'm sent to a reachable, free charger | Must | Planned | services/dispatch-optimizer/ | | | |
| F-10 | Secure multi-tenant API (OIDC, RBAC, RLS, rate limit, keyset) | As a tenant my data is isolated | Must | Planned | services/fleet-api/ | | | |
| F-11 | Audit log, location masking, crypto-shred erasure | As a DPO I can prove compliance | Must | Planned | services/fleet-api/ | | | |
| F-12 | Batch analytics on Iceberg | As finance I see monthly charging cost and savings | Must | Planned | batch/ | | | |
| F-13 | Observability dashboards | As SRE I find the cause of a latency spike | Must | Planned | infra/observability/ | | | |
| F-14 | SoH ML model vs baseline | As a lessor I get more accurate SoH than the odometer heuristic | Should | Planned | ml/, services/battery-intel/ | | | |
| F-15 | 7-day fault-risk prediction | As a manager I service vehicles before they break down | Should | Planned | ml/, services/battery-intel/ | | | |
| F-16 | Health-aware charging cost | As a manager my plans don't trade battery life for pennies | Should | Planned | services/dispatch-optimizer/ | | | |
| F-17 | Fault-signature similarity (pgvector) | As a technician I see similar past cases | Should | Planned | services/battery-intel/ | | | |
| F-18 | Fleet Copilot with approval + audit | As a dispatcher I ask questions in plain language and approve suggested actions | Should | Planned | services/copilot-agent/ | | | |
| F-19 | Charging carbon report | As a sustainability lead I see CO₂ per route | Could | Planned | batch/ | | | |
| F-20 | V2G / bidirectional charging | — | Won't | — | — | — | — | — |
