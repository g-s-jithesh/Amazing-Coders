# web — CLAUDE.md

React + TypeScript (strict) + Vite. This is the ops console the judges will see first. It must be **clear, fast with 100K vehicles, and honest**: every number shown comes from the API with its method and timestamp. Pages and requirements are in root §10.

## Layout

```
src/
  app/            # router, providers (QueryClient, Auth, Theme), layout shell
  pages/          # FleetMap, BatteryHealth, VehicleDetail, DispatchPlanner, Alerts, Copilot, Admin
  features/       # feature-scoped components + hooks (map/, soh/, dispatch/, alerts/, copilot/, audit/)
  components/     # shared UI primitives only
  api/            # GENERATED client + types from docs/api/openapi.yaml (openapi-typescript); don't hand-edit
  lib/            # auth (oidc-client-ts), ws client, formatters (units, ₹ from paise, IST display)
tests/            # vitest unit; e2e/ Playwright (demo flows); pact/ consumer contracts
```

## Rules

- **No business logic in the UI.** Savings, SoH, risk and costs are computed server-side. The UI formats and visualises them.
- **Types come from OpenAPI.** Regenerate with `npm run gen:api` after an API change. Never write request/response types by hand.
- **Auth:** OIDC Authorization Code + PKCE against Keycloak. **Tokens in memory only** (no localStorage/sessionStorage) and silent renew. On a 401, re-auth; on a 403, show "not permitted"; on a 404, show "not found" (don't leak existence).
- **Money** arrives as paise and is formatted with `Intl.NumberFormat('en-IN', {style:'currency', currency:'INR'})`. **Time** arrives as UTC and is displayed in the user's timezone (default IST) with the zone shown.
- **Location masking:** the API may return `geohash5` instead of lat/lon. Components must render both (draw the cell polygon for masked positions). Never assume coordinates exist.

## Map at 100K vehicles

- MapLibre GL with **GeoJSON source + clustering** (or supercluster in a Web Worker) and **WebGL layers**, never DOM markers.
- Open tiles only (declare the tile source and its licence in `docs/declarations.md`).
- WebSocket deltas are buffered and applied at most once per second with `setData` on a worker-built FeatureCollection. Only the viewport's detail is shown at high zoom.
- Reconnect with exponential backoff + jitter. Show a "live / reconnecting / stale (last update hh:mm:ss)" indicator.

## Page-specific notes

- **Dispatch planner:**
  - a Gantt of chargers × 15-min slots
  - the site load curve with the power-cap line
  - a cost breakdown (energy / degradation / penalty)
  - baseline vs plan
  - an `INFEASIBLE` / `GREEDY_FALLBACK` badge
  - an Approve button (visible only with `dispatch:approve`, sends an `Idempotency-Key`)
- **Vehicle detail:** SoC, temperature and imbalance charts over time (raw / 1m / 15m resolution toggle), a DTC timeline, the decoded fault + runbook excerpt, and "similar past faults" with their similarity score.
- **Copilot:** streamed answers, an expandable tool-call trace with citations, and approval cards that deep-link to the planner (approval happens there, not in chat).
- **Alerts:** severity colour + icon + text (not colour alone), ack with a note, and filters that stay in the URL.

## Quality

- Accessibility: WCAG 2.1 AA contrast, full keyboard navigation, focus rings, `aria-live` for new critical alerts.
- Performance: route-level code splitting. Charts downsample to ≤ 2,000 points. Lighthouse perf ≥ 80 on the dashboard (record with `/evidence`).
- Playwright e2e scripts mirror `docs/demo-script.md` and save screenshots to `docs/solution-document/screenshots/` for the Solution Document.

## Test focus

- Formatters (paise → ₹, UTC → IST, units).
- Masked vs precise location rendering.
- WS reconnect and delta batching.
- Permission-gated UI (the Approve button).
- Pact consumer tests for every API call used.
- Playwright: map loads, alert appears, plan approve flow.
