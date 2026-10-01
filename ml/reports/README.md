# ML reports

Current `soh/report.json` and `fault_risk/report.json` come from a **local laptop run** (Windows 11, Python 3.12,
LightGBM 4.7.0, see `env` inside each report) on the committed datasets (`ml/data/`, built by `ml/build_dataset.py`,
log in `dataset_build.log`). The Google Colab run (`ml/train_colab.ipynb`) uses the same seed and data and replaces these
files; expect identical or near-identical metrics. Protocol, splits and the leakage guards are in the module docstrings of
`build_dataset.py` and `train.py`.

Read the comparison honestly: on this simulator the raw coulomb count is already very accurate for SoH, so the LightGBM
model's value over it is small; the fault-risk model beats threshold rules on PR-AUC but misses the recall-at-precision-0.5
and lead-time targets.
