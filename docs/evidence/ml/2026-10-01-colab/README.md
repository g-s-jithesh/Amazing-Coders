# ML evidence (Colab run, 2026-10-01)

Raw reports and model files in this folder; protocol in `ml/train.py` / `ml/build_dataset.py`. Data: 1,200 simulated vehicles, 28 days.
Test split is grouped by vehicle (test vehicles: 240).

## SoH regressor (F-14), evaluated against exact truth on 240 test vehicles
| | MAE (pp) | 95 % CI | P90 abs err (pp) |
|---|---|---|---|
| LightGBM | 0.398 | 0.357-0.441 | 0.887 |
| linear fade by age | 2.404 | 2.152-2.692 | 5.360 |
| raw coulomb count | 0.286 | 0.255-0.316 | 0.603 |

Target MAE <= 2 pp: met by the model **and** by raw coulomb counting, which is more accurate than the model here.

## 7-day fault risk (F-15), test positive rate 22.9%
| | PR-AUC | recall at precision >= 0.5 | median lead time (h) |
|---|---|---|---|
| LightGBM | 0.569 | 0.43 | 39.0 |
| threshold rules | 0.359 | 0.17 (precision 1.0) | 22.2 |
| random | 0.229 | - | - |

Targets (recall >= 0.7 at precision >= 0.5, lead time >= 48 h): **not met**.
Simulated faults with precursors; one random-fault regime (8 onsets/vehicle-year); results do not transfer to real fleets.
