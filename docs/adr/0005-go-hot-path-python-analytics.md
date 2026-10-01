# ADR-0005: Go for the hot path, Python for analytics and APIs

- **Status:** Accepted
- **Date:** 2026-10-01
- **Deciders:** Team Amazing Coders (drafted with Claude)
- **Related:** CLAUDE.md §3.1

## Context
Ingest and stream processing handle 10^4-10^5 events/s per core budget; SoH, optimisation and ML are numpy/LightGBM work.

## Options considered
1. **All Python**: one language, but GIL-bound hot path needs many processes per core budget.
2. **All Go**: fast, but the numerical and ML ecosystem is thin.
3. **Go hot path + Python everything else (chosen)**, joined by a versioned Protobuf contract (`libs/proto`, buf).

## Decision
Go: simulator, ingest-gateway, stream-processor. Python 3.12: battery-intel, dispatch-optimizer, ml. The only
coupling is Protobuf over Kafka (`telemetry`, `sessions`, `alerts`, `fleet.vehicle`, `dispatch.commands`).
Layering is hexagonal inside each service (pure `domain/`, ports in `application/`, adapters in `infrastructure/`).

## Consequences
- Two toolchains in CI (Go, uv); generated code for both languages from one schema (`make proto-gen`).
- Backward-compatible schema evolution only (`buf breaking` on pull requests).
