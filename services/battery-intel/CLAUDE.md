# battery-intel — CLAUDE.md

Python 3.12 / FastAPI + worker. Answers **Q1: which batteries are unhealthy or about to fault, and why** (root §1.2). It covers SoH estimation, RUL, 7-day fault risk, DTC decoding and runbook retrieval, and fault-signature similarity (pgvector). Innovation pillar #2 lives here.

## Owns

Postgres schema `battery`:
- `soh_estimate` (pack_id, as_of, soh_pct, method, ci_low, ci_high, session_id)
- `pack_kf_state` (Kalman state per pack)
- `fault_risk_score` (vehicle_id, as_of, p_7d, model_version, top_features jsonb)
- `dtc_code`, `vehicle_dtc_event`
- `runbook_chunk` (embedding vector(384))
- `fault_signature` (embedding vector(64), label, lead_time_h)

## Layout

```
app/
  api/v1/internal/        # called by fleet-api / copilot-agent only (service JWT + user token passthrough)
  application/            # EstimateSohFromSession, ScoreFleetRisk, ExplainDtc, FindSimilarFaults, SearchRunbooks
  domain/
    soh/                  # coulomb counting, OCV→SoC lookup, session quality gates, 1-D Kalman filter, RUL fit
    dtc/                  # DTC decode (system/subsystem), severity mapping
    signature/            # 48 h window → 64-d vector (pure numpy)
  infrastructure/         # repositories, kafka consumer, model loader, embedding model, pgvector queries
  workers/session_consumer.py   # battery.sessions.v1 → SoH update
models/                   # model registry.json (version → path + sha256); artefacts pulled from object storage
```

## SoH method (physics first, ML second)

1. **Session quality gate.** Use a charge session only if ΔSoC ≥ 20 pp, pack temperature is 15–35 °C, and there is a rest of ≥ 30 min before or after (for an OCV-based SoC). Otherwise skip it and record the reason.
2. **ΔSoC** comes from rest-point OCV via the model's OCV→SoC table when available (the BMS SoC is itself an estimate). Fall back to the BMS SoC with a larger measurement variance.
3. `Q_meas = |ΔAh| / ΔSoC`, and `SoH_obs = Q_meas / Q_nominal`.
4. **Kalman filter** per pack: state = SoH, a small process noise (fade per day), and a measurement noise from session quality. Output SoH ± 1.96σ as a CI.
5. **RUL:** fit SoH vs (√t, EFC) per pack; days until the EOL threshold (config, default 80%).
6. The **ML SoH model** (from `ml/`) is served alongside as `method=ml`. The UI shows the physics estimate by default, and the model where it beats the baseline (evidence in `ml/reports/`).

## Fault risk (7-day)

- LightGBM artefact from `ml/`, loaded by version with a **sha256 check**. Refuse to serve on a mismatch.
- **Features come from the shared module `libs/py-common/kilowatt/features/`**. It's the same code as training, which prevents train/serve skew. Never reimplement a feature here.
- Return `p_7d`, the model version, and the top-3 contributing features (SHAP or gain-based) for the "why" in the UI.

## DTC and runbooks (RAG)

- `dtc_code` is seeded from `data/reference/dtc_catalogue.csv`. The decode is deterministic (the table plus system letter and code structure). No LLM is needed to decode.
- Runbook RAG:
  - chunk `data/runbooks/*.md` by heading (≤ 500 tokens)
  - embed with a local sentence-transformers model (`all-MiniLM-L6-v2`, 384-d, cosine)
  - HNSW index
  - query = DTC description + vehicle model + recent alert evidence
  - filter by system/model and return the top-k with scores
- Runbooks are **our synthetic content**. Mark them so in the corpus front-matter.

## Fault-signature similarity

- Signature = ~16 signals (temp delta, dT/dt, imbalance, isolation, interlock flaps, aux V, C-rate, SoC dwell…) × {mean, std, slope, max} over 48 h → z-normalised by the vehicle-model population → L2-normalised → 64-d.
- Labelled signatures are stored when a fault DTC occurs (window = 48 h before the DTC; label = fault type; lead time).
- Query: each vehicle's current rolling signature → kNN (cosine, HNSW) over labelled signatures, **filtered by tenant**. With filters, raise `hnsw.ef_search` and enable pgvector iterative index scans (pgvector ≥ 0.8) so filtered queries still return k rows. Measure recall against an exact scan and put it in `/evidence`.

## Rules

- **Never read `data/ground_truth/`**, and never use the simulator's ageing parameters. All evaluation happens in `ml/`.
- Internal endpoints still enforce the tenant: they take the caller's user token (passed through by fleet-api or copilot) and set RLS.
- Numbers shown to users (SoH, p_7d) always carry their method, version and timestamp.

## Test focus

- Coulomb counting against an analytic session.
- Quality-gate rejections.
- Kalman convergence and CI coverage on synthetic series.
- RUL on a known curve.
- Model sha mismatch → refuse.
- Feature parity test (same input → same features as `ml/`).
- DTC decode table.
- RAG top-k contains the expected runbook for each DTC in the catalogue (a small golden set).
- kNN recall vs exact scan.
- Tenant filter on similarity.
- Consumer idempotency (the same session twice → one estimate).
