# ML reports

`soh/report.json` and `fault_risk/report.json` are from the **Google Colab run** (`ml/train_colab.ipynb`, Python 3.13.15,
LightGBM 4.6.0, x86_64; see `env` in each report) on the committed datasets (`ml/data/`, built by `ml/build_dataset.py`,
log in `dataset_build.log`). An earlier local run (Windows, Python 3.12, LightGBM 4.7.0) gave identical metrics. Model files
and their sha256 are in `docs/evidence/ml/2026-10-01-colab/` (`ml/artefacts/` is gitignored). Protocol, splits and leakage
guards are in the docstrings of `build_dataset.py` and `train.py`.

Read the comparison honestly: on this simulator the raw coulomb count is already very accurate for SoH, so the LightGBM
model adds little over it; the fault-risk model beats threshold rules on PR-AUC but misses the recall-at-precision-0.5 and
lead-time targets.
