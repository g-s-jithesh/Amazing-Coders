# Demo script (about 4 minutes)

Prereqs: `make up` (Docker capped to about 5 GB, see README), `make seed`, open `web/index.html` in a browser.
Console defaults: tenant "Synthetic Logistics A", role dispatcher.

| Time | Do | Say |
|---|---|---|
| 0:00 | Slide / voice: depot of EVs all plugging in at the 18:00-22:00 peak. | Charging "on arrival" pays peak tariff, can exceed connector capacity, and ages the battery faster; battery faults are found only after a breakdown. |
| 0:30 | `make sim RATE_HZ=0.01 INJECT=<vin>:cooling_degradation PRECURSOR=30m START=<after last run>` (VIN printed by the README demo section). | 100,000 simulated vehicles in three OEM formats, with duplicates, late data and malformed payloads. |
| 1:00 | Console, **Real-time alerts** panel. | A thermal fault injected into one vehicle is detected from its precursor; the table shows the gateway-to-alert latency. |
| 1:30 | Click the VIN. **Vehicle & DTC**: SoH with 95 % interval. Type `P0A7E`, Decode. | Battery health from coulomb counting plus a Kalman filter; the fault code is decoded from the catalogue (marked unverified). |
| 2:00 | **Battery health**: distribution and lowest-SoH packs. | Which packs are weakest across the fleet right now. |
| 2:30 | **Charging dispatch planner**: pick a depot, Generate plan. | Dynamic programming per vehicle, coupled by site power and connector limits; cost is energy plus battery wear. Zero missed departures. |
| 3:15 | Approve. | Only a dispatcher can approve; it goes through a transactional outbox to `dispatch.commands.v1`. |
| 3:40 | Show `docs/evidence/dispatch/2026-10-01-backtest-5x30/README.md` and `ml/reports/*/report.json`. | Measured results: energy cost -10.75 % over 30 days, SoH model vs baselines, fault-risk PR-AUC vs rules. Not done: public API, map, cluster load test. |
