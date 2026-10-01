"""ML guard rails: leakage assertions, deterministic training, and the report contract."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import numpy as np
import pandas as pd
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import build_dataset as bd
import train

DATA = Path(__file__).resolve().parents[1] / "data"


def test_soh_label_is_not_a_feature() -> None:
    assert "soh_true_pct" not in train.SOH_FEATURES and "soh_true" not in bd.OBSERVABLE
    assert not any("true" in f for f in train.SOH_FEATURES + train.FAULT_FEATURES)


def test_leakage_check_rejects_unexpected_columns() -> None:
    ok = pd.DataFrame({c: [0] for c in bd.OBSERVABLE | {"soh_true", "ts_ist", "evt", "lat", "lon"}})
    bd.leakage_check(ok)
    with pytest.raises(AssertionError):
        bd.leakage_check(ok.assign(dtc_ts=[1]))  # a deliberately leaky column


def test_vehicle_split_is_disjoint_and_deterministic() -> None:
    vins = np.array([f"V{i % 50:03d}" for i in range(500)])
    a, b = train.split_vehicles(vins), train.split_vehicles(vins)
    assert a == b
    assert not (a["train"] & a["val"]) and not (a["train"] & a["test"]) and not (a["val"] & a["test"])
    assert len(a["train"] | a["val"] | a["test"]) == 50


def test_fault_windows_never_use_the_future() -> None:
    n = 28 * bd.ROWS_PER_DAY
    df = pd.DataFrame(
        {
            "pack_temp_max_c": np.full(n, 30.0), "ambient_c": np.full(n, 25.0), "cell_v_min_mv": np.full(n, 3800),
            "cell_v_max_mv": np.full(n, 3810), "isolation_kohm": np.full(n, 3000.0), "hv_interlock_ok": ["true"] * n,
            "aux_12v_v": np.full(n, 13.9), "soc_pct": np.full(n, 60.0), "dtc": [None] * n,
        }
    )  # fmt: skip
    first = 10 * bd.ROWS_PER_DAY + 5
    df.loc[first:, "isolation_kohm"] = 10.0  # the fault shows up only from `first` on
    df.loc[first:, "dtc"] = "P0AA6"
    rows = bd.fault_rows(df, "V")
    assert all(r["day"] * bd.ROWS_PER_DAY <= first for r in rows)  # samples after the first DTC are dropped
    last = max(rows, key=lambda r: r["day"])
    assert last["day"] == 10 and last["label"] == 1 and last["iso_min"] == 3000.0  # features end before the fault


@pytest.mark.skipif(not (DATA / "soh.parquet").exists(), reason="datasets not built")
def test_training_is_deterministic_and_reports_are_complete(tmp_path: Path) -> None:
    out1, out2 = tmp_path / "a", tmp_path / "b"
    r1 = train.train_soh(DATA, out1, tmp_path)
    r2 = train.train_soh(DATA, out2, tmp_path)
    assert r1["results"]["model"]["mae_pp"] == r2["results"]["model"]["mae_pp"]
    f = train.train_fault(DATA, out1, tmp_path)
    for k in ("model", "baseline_threshold_rules"):
        assert "pr_auc" in f["results"][k]
    assert f["artefact"]["sha256"] == train.sha256(tmp_path / "fault_risk_lgbm.txt")
    assert json.loads((out1 / "soh/report.json").read_text())["target_mae_pp"] == 2.0
