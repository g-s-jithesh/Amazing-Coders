# Architecture (C4 level 2, as built)

```mermaid
flowchart LR
  subgraph Sources
    SIM[simulator<br/>100K EVs, 3 OEM formats, noise, faults]
  end
  SIM -- MQTT oem_a / oem_c --> GW
  SIM -- HTTPS batch oem_b --> GW
  GW[ingest-gateway<br/>adapters, VIN/DTC validation,<br/>dedup, DLQ, back-pressure] -- telemetry.canonical.v1 --> K[(Kafka)]
  GW -- telemetry.dlq.v1 --> K
  K --> SP[stream-processor<br/>rules, alert state machine,<br/>sessions, rollups, top-K]
  SP -- alerts.v1 / battery.sessions.v1 --> K
  SP --> R[(Redis<br/>latest state, geo)]
  K --> RAW[raw-sink] --> SC[(ScyllaDB<br/>raw telemetry, 7 d TTL)]
  K -- battery.sessions.v1 + fleet.vehicle.v1 --> BI[battery-intel<br/>coulomb SoH + Kalman,<br/>DTC decode]
  BI --> PG[(Postgres 16 + RLS<br/>schemas fleet / battery / dispatch)]
  DO[dispatch-optimizer<br/>DP + Lagrangian planner,<br/>plan lifecycle] --> PG
  DO -- outbox relay: dispatch.commands.v1 --> K
  UI[web console<br/>single HTML page] -- REST --> BI
  UI -- REST --> DO
  BI -. alerts.v1 peek .-> K
  ML[ml/<br/>LightGBM SoH + 7-day fault risk<br/>trained in Colab] -. artefacts + reports .-> BI
```

## Event path

`vehicle -> MQTT/HTTPS -> ingest-gateway -> Kafka (key = VIN) -> stream-processor -> Redis / alerts.v1`.
Measured locally: gateway to Kafka about 20 ms (`docs/evidence/load/`); end-to-end alert latency under laptop load is
reported there with its caveats; the p95 < 5 s target is deferred to a cluster run.

## What is not in this picture (not built)
fleet-api (public API, Keycloak/JWT), copilot-agent, batch/Iceberg, en-route A* dispatch, Helm/Terraform.
See `docs/risks.md`.
