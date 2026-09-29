# ml — CLAUDE.md

Training and **evaluation** of the SoH regressor and the 7-day fault-risk model, the copilot eval harness, and the reports the Solution Document §11 quotes (root §8). **This is the only place allowed to use simulator ground truth** (via `make ml-train` / `make ml-eval` at runtime; Claude itself doesn't open those files, and the guard hook enforces that).

## Layout

```
ml/
  datasets/        # build training/eval frames from Iceberg feature tables (+ labels, see below)
  features/        # thin re-export of libs/py-common/kilowatt/features (single source of truth)
  models/
    soh/           # train.py, baseline.py (odometer-linear, raw coulomb count)
    fault_risk/    # train.py, baseline.py (threshold rules mirroring stream-processor config)
  evaluate.py      # common metrics, bootstrap CIs, report writer
  leakage.py       # leakage assertions (run in CI)
  registry.json    # version → artefact path, sha256, training data snapshot id, metrics
  reports/         # <model>/<version>/report.{json,html}
  copilot_eval/    # *.yaml cases + runner (uses FakeLLM in CI; real provider on demand)
  artefacts/       # gitignored; published to object storage
```

## Labels: be realistic and honest

- **SoH training labels** = a sparse subset that simulates **workshop capacity tests** (e.g. ~2% of packs, tested every ~90 days, with measurement noise). That's how real fleets get labels. Build it in `datasets/` from ground truth with a fixed seed.
- **Evaluation** uses the full ground truth (all packs, all days) to report true error. State this protocol in the report.
- **Fault labels:** positive if an injected fault's `dtc_ts` falls within [t, t+7 d]. Features must end at t (not after).

## Splits and leakage (all enforced by `leakage.py` in CI)

- **Time-based** split: train before cutoff T, test after. **Grouped by vehicle:** no VIN appears in both train and test (GroupKFold for CV).
- Assert that the feature-window end ≤ the label-window start.
- Assert that no feature column derives from ground-truth tables (check lineage by column-name allow-list).
- Assert that the optimiser and service configs don't contain simulator ageing coefficients (compare the config values against the simulator's hidden parameters and fail on a match).

## Models

- LightGBM with fixed seeds. Pin versions in `pyproject.toml`. Hyperparameter search is small and logged. No test-set peeking: tune on validation only.
- **Report vs baseline, always:**
  - SoH: MAE (pp) and P90 absolute error, with bootstrap 95% CIs; per vehicle model; a calibration plot of the CI coverage from battery-intel's Kalman filter
  - fault risk: PR-AUC, recall at precision ≥ 0.5, median lead time (h), and alerts per 1,000 vehicles per week (the operational cost)
- Explainability: global feature importance plus per-prediction top-3 (the same method battery-intel serves).

## Artefacts and serving contract

- Save the artefact + sha256 + feature list/version + training snapshot id to `registry.json`. battery-intel **refuses to load** on a sha mismatch or a feature-version mismatch.
- **Train/serve parity test:** compute features for the same input through `ml/` and the battery-intel serving path, and assert equality.

## Evidence

Every metric that leaves this folder goes through `/evidence ml <label>`. Copy the report JSON into `docs/evidence/ml/...` and summarise it there. Never paste numbers into docs by hand.

## Test focus

- Leakage assertions (they must fail on a deliberately leaky fixture).
- Deterministic training on a tiny dataset (same seed → same metrics).
- Metric functions against sklearn references.
- Registry integrity.
- Feature parity.
- The copilot eval runner with FakeLLM.
