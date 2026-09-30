# Evidence index

Every number in the README, the Solution Document or the demo links to a folder here. Create runs with `/evidence <kind> [label]`. Never edit raw files; failed runs stay recorded.

| Date | Kind | Label | Headline result | Verdict | Folder |
|------|------|-------|-----------------|---------|--------|
| 2026-09-30 | load | sim-kafka-direct-local | 18,142,411 events superseded run: loss check not captured | — (not used) | [folder](load/2026-09-30_sim-kafka-direct-local/) |
| 2026-09-30 | load | sim-kafka-direct-local-2 | simulator generation 152,628 eps steady (peak 355K), 0 loss, 1 local broker | PASS (simulator target; not the e2e NFR) | [folder](load/2026-09-30_sim-kafka-direct-local-2/) |
| 2026-09-30 | load | gateway-e2e-local | 0 of 1,240,811 unaccounted; produce p99 ≤ 4.1 s (stall cluster) | PASS accounting / CONCERN latency | [folder](load/2026-09-30_gateway-e2e-local/) |
| 2026-09-30 | load | gateway-e2e-local-2 | fresh broker: same stall → aged-broker hypothesis refuted | PASS accounting / CONCERN latency | [folder](load/2026-09-30_gateway-e2e-local-2/) |
| 2026-09-30 | load | gateway-e2e-local-3 | files written outside OneDrive: 0 unaccounted, produce p50/p95/p99 ≤ 32/128/512 ms | PASS | [folder](load/2026-09-30_gateway-e2e-local-3/) |
| 2026-09-30 | load | sp-e2e-1-coupled | demo P0A7E alert 23.3 s gateway→Kafka (target < 5 s) | FAIL | [folder](load/2026-09-30_sp-e2e-1-coupled/) |
| 2026-09-30 | load | sp-e2e-2-async-raw | demo alert 69.9 s | FAIL | [folder](load/2026-09-30_sp-e2e-2-async-raw/) |
| 2026-09-30 | load | sp-e2e-3-sink-timing | demo alert 50.6 s | FAIL | [folder](load/2026-09-30_sp-e2e-3-sink-timing/) |
| 2026-09-30 | load | sp-e2e-4-no-tombstones | demo alert 32.9 s | FAIL | [folder](load/2026-09-30_sp-e2e-4-no-tombstones/) |
| 2026-10-01 | load | sp-e2e-5-split-roles | demo P0A7E FIRING alert 18 ms gateway→processed, 20 ms →Kafka | PASS (alert path); CONCERN (processor lag spike) | [folder](load/2026-10-01_sp-e2e-5-split-roles/) |
| 2026-10-01 | load | sp-e2e-6-dedup-after-ack | 0 unaccounted | PASS (no loss); CONCERN (dedup rate) | [folder](load/2026-10-01_sp-e2e-6-dedup-after-ack/) |
| 2026-10-01 | load | sp-e2e-7-inflight-dedup | 0 unaccounted of 1,896,122 | PASS | [folder](load/2026-10-01_sp-e2e-7-inflight-dedup/) |
