# Evidence index

Every number in the README, the Solution Document or the demo links to a folder here. Create runs with `/evidence <kind> [label]`. Never edit raw files; failed runs stay recorded.

| Date | Kind | Label | Headline result | Verdict | Folder |
|------|------|-------|-----------------|---------|--------|
| 2026-09-30 | load | sim-kafka-direct-local | 18,142,411 events superseded run: loss check not captured | — (not used) | [folder](load/2026-09-30_sim-kafka-direct-local/) |
| 2026-09-30 | load | sim-kafka-direct-local-2 | simulator generation 152,628 eps steady (peak 355K), 0 loss, 1 local broker | PASS (simulator target; not the e2e NFR) | [folder](load/2026-09-30_sim-kafka-direct-local-2/) |
| 2026-09-30 | load | gateway-e2e-local | 0 of 1,240,811 unaccounted; produce p99 ≤ 4.1 s (stall cluster) | PASS accounting / CONCERN latency | [folder](load/2026-09-30_gateway-e2e-local/) |
| 2026-09-30 | load | gateway-e2e-local-2 | fresh broker: same stall → aged-broker hypothesis refuted | PASS accounting / CONCERN latency | [folder](load/2026-09-30_gateway-e2e-local-2/) |
| 2026-09-30 | load | gateway-e2e-local-3 | files written outside OneDrive: 0 unaccounted, produce p50/p95/p99 ≤ 32/128/512 ms | PASS | [folder](load/2026-09-30_gateway-e2e-local-3/) |
