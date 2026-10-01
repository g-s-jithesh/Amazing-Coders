"""Train and evaluate both models against their baselines. Runs anywhere (laptop, Colab):

    python train.py --data data --out reports

Writes reports/soh/report.json, reports/fault_risk/report.json and artefacts/*.txt (LightGBM models) with
sha256 hashes in artefacts/registry.json. Seeds are fixed; metrics carry bootstrap 95 % CIs.

Splits (see build_dataset.py): grouped by vehicle (no VIN in both train and test) and, for fault risk,
time-based (train samples from days <= 12, test samples from days >= 15 so that 7-day label windows
of the train set end before the test features start). Tuning uses validation vehicles only.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
from datetime import UTC, datetime
from pathlib import Path

import lightgbm as lgb
import numpy as np
import pandas as pd
from sklearn.metrics import average_precision_score, precision_recall_curve

SEED = 7
SOH_FEATURES = ["age_days", "capacity_kwh", "efc_week", "temp_mean", "ambient_mean", "soc_gt80_frac", "charge_kw_mean", "coulomb_soh", "model_cat"]  # fmt: skip
FAULT_FEATURES = [
    "td_mean", "td_max", "td_slope_per_h", "td_mean_24h", "imb_mean", "imb_max", "imb_slope_per_h", "imb_mean_24h",
    "iso_min", "iso_mean", "iso_slope_per_h", "iso_min_24h", "hvil_false_frac", "hvil_flaps", "aux_min", "aux_mean",
    "soc_mean", "soc_gt80_frac",
]  # fmt: skip
TRAIN_LAST_DAY, TEST_FIRST_DAY = 12, 15
# Threshold rules mirroring services/stream-processor/config/rules.yaml (warning levels).
RULES = {"imb_max": 50.0, "iso_min": 500.0, "aux_min": 11.8, "td_max": None}


def boot_ci(values: np.ndarray, stat, n: int = 1000) -> tuple[float, float]:
    rng = np.random.default_rng(SEED)
    idx = rng.integers(0, len(values), (n, len(values)))
    s = np.array([stat(values[i]) for i in idx])
    return float(np.percentile(s, 2.5)), float(np.percentile(s, 97.5))


def split_vehicles(vins: np.ndarray) -> dict[str, set[str]]:
    rng = np.random.default_rng(SEED)
    v = np.array(sorted(set(vins)))
    rng.shuffle(v)
    n = len(v)
    return {"train": set(v[: int(0.6 * n)]), "val": set(v[int(0.6 * n) : int(0.8 * n)]), "test": set(v[int(0.8 * n) :])}


def sha256(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def env() -> dict[str, str]:
    return {
        "python": platform.python_version(), "lightgbm": lgb.__version__, "machine": platform.machine(),
        "processor": platform.processor(), "at": datetime.now(UTC).isoformat(timespec="seconds"),
    }  # fmt: skip


# ----------------------------------------------------------------------------------------------- SoH


def train_soh(data: Path, out: Path, art: Path) -> dict:
    df = pd.read_parquet(data / "soh.parquet")
    df["model_cat"] = df["model_code"].astype("category").cat.codes
    parts = split_vehicles(df["vin"].to_numpy())
    tr, va, te = (df[df["vin"].isin(parts[k])].copy() for k in ("train", "val", "test"))

    # Workshop-test labels: 30 % of the training vehicles, +-0.5 pp noise (the real-fleet label regime).
    rng = np.random.default_rng(SEED)
    lab = tr.sample(frac=0.30, random_state=SEED).copy()
    lab["y"] = lab["soh_true_pct"] + rng.normal(0, 0.5, len(lab))
    val = va.sample(frac=0.30, random_state=SEED).copy()  # validation is labelled the same way
    val["y"] = val["soh_true_pct"] + rng.normal(0, 0.5, len(val))

    # Baselines. (a) linear fade by age (odometer is zero at trace start); (b) raw coulomb count.
    slope, icpt = np.polyfit(lab["age_days"], lab["y"], 1)
    base_age = lambda d: icpt + slope * d["age_days"]
    base_coul = lambda d: d["coulomb_soh"].fillna(base_age(d))

    best, best_mae = None, 1e9
    for leaves in (4, 8, 16):
        for lr in (0.03, 0.1):
            m = lgb.LGBMRegressor(
                n_estimators=300, learning_rate=lr, num_leaves=leaves, min_child_samples=5, random_state=SEED, verbose=-1
            )
            m.fit(lab[SOH_FEATURES], lab["y"])
            mae = float(np.abs(m.predict(val[SOH_FEATURES]) - val["y"]).mean())
            if mae < best_mae:
                best, best_mae = (m, {"num_leaves": leaves, "learning_rate": lr}), mae
    model, params = best

    def metrics(pred: np.ndarray, truth: np.ndarray) -> dict:
        err = np.abs(pred - truth)
        return {
            "mae_pp": float(err.mean()), "mae_ci95": boot_ci(err, np.mean),
            "p90_abs_err_pp": float(np.percentile(err, 90)), "p90_ci95": boot_ci(err, lambda x: np.percentile(x, 90)),
        }  # fmt: skip

    truth = te["soh_true_pct"].to_numpy()
    res = {
        "model": metrics(model.predict(te[SOH_FEATURES]), truth),
        "baseline_linear_age": metrics(base_age(te).to_numpy(), truth),
        "baseline_raw_coulomb": metrics(base_coul(te).to_numpy(), truth),
    }
    per_model = {}
    for code, g in te.groupby("model_code"):
        per_model[code] = {
            "n": len(g), "model_mae_pp": float(np.abs(model.predict(g[SOH_FEATURES]) - g["soh_true_pct"]).mean()),
            "linear_age_mae_pp": float(np.abs(base_age(g) - g["soh_true_pct"]).mean()),
        }  # fmt: skip
    imp = dict(sorted(zip(SOH_FEATURES, model.booster_.feature_importance("gain").round(1).tolist()), key=lambda kv: -kv[1]))
    path = art / "soh_lgbm.txt"
    model.booster_.save_model(str(path))
    report = {
        "kind": "soh_regressor", "protocol": "labels: 30 % of train vehicles with +-0.5 pp noise; evaluation: exact truth on all test vehicles",
        "n_vehicles": {k: len(v) for k, v in parts.items()}, "n_labelled_train": len(lab), "n_eval": len(te),
        "params": params, "val_mae_pp": best_mae, "results": res, "per_vehicle_model": per_model, "feature_gain": imp,
        "target_mae_pp": 2.0, "artefact": {"path": path.name, "sha256": sha256(path)}, "env": env(),
    }  # fmt: skip
    (out / "soh").mkdir(parents=True, exist_ok=True)
    (out / "soh/report.json").write_text(json.dumps(report, indent=1))
    return report


# ------------------------------------------------------------------------------------- fault risk


def operating_point(y: np.ndarray, score: np.ndarray, min_precision: float = 0.5) -> dict:
    p, r, thr = precision_recall_curve(y, score)
    ok = np.nonzero(p[:-1] >= min_precision)[0]
    if len(ok) == 0:
        return {"threshold": None, "precision": None, "recall": 0.0}
    i = ok[np.argmax(r[:-1][ok])]
    return {"threshold": float(thr[i]), "precision": float(p[i]), "recall": float(r[i])}


def rule_score(d: pd.DataFrame) -> np.ndarray:
    """Baseline: warning-level threshold rules (max of the normalised exceedances, 0 when none exceeded)."""
    s = np.zeros(len(d))
    s = np.maximum(s, (d["imb_max"].to_numpy() >= RULES["imb_max"]) * d["imb_max"].to_numpy() / RULES["imb_max"])
    s = np.maximum(s, (d["iso_min"].to_numpy() <= RULES["iso_min"]) * RULES["iso_min"] / np.maximum(d["iso_min"].to_numpy(), 1))
    s = np.maximum(s, (d["aux_min"].to_numpy() <= RULES["aux_min"]) * RULES["aux_min"] / np.maximum(d["aux_min"].to_numpy(), 1))
    s = np.maximum(s, (d["hvil_false_frac"].to_numpy() > 0) * 1.0)
    return s


def train_fault(data: Path, out: Path, art: Path) -> dict:
    df = pd.read_parquet(data / "fault_risk.parquet")
    parts = split_vehicles(df["vin"].to_numpy())
    tr = df[df["vin"].isin(parts["train"]) & (df["day"] <= TRAIN_LAST_DAY)]
    va = df[df["vin"].isin(parts["val"]) & (df["day"] > TRAIN_LAST_DAY)]
    te = df[df["vin"].isin(parts["test"]) & (df["day"] >= TEST_FIRST_DAY)]
    assert not (set(tr["vin"]) & set(te["vin"])), "leakage: a vehicle is in both train and test"
    assert tr["day"].max() < te["day"].min(), "leakage: train and test days overlap"

    best, best_ap = None, -1.0
    for leaves in (4, 8, 16):
        for lr in (0.03, 0.1):
            m = lgb.LGBMClassifier(
                n_estimators=300, learning_rate=lr, num_leaves=leaves, min_child_samples=10, random_state=SEED, verbose=-1
            )
            m.fit(tr[FAULT_FEATURES], tr["label"])
            ap = average_precision_score(va["label"], m.predict_proba(va[FAULT_FEATURES])[:, 1])
            if ap > best_ap:
                best, best_ap = (m, {"num_leaves": leaves, "learning_rate": lr}), ap
    model, params = best

    y = te["label"].to_numpy()
    ml_score = model.predict_proba(te[FAULT_FEATURES])[:, 1]
    rl_score = rule_score(te)

    def summarise(score: np.ndarray) -> dict:
        op = operating_point(y, score)
        out_ = {"pr_auc": float(average_precision_score(y, score)), "operating_point_p>=0.5": op}
        if op["threshold"] is not None:
            flagged = score >= op["threshold"]
            lead = te["lead_h"].to_numpy()[flagged & (y == 1)]
            out_["median_lead_time_h"] = float(np.nanmedian(lead)) if len(lead) else None
            out_["flags_per_1000_vehicle_days"] = float(1000 * flagged.sum() / len(te))
        return out_

    imp = dict(sorted(zip(FAULT_FEATURES, model.booster_.feature_importance("gain").round(1).tolist()), key=lambda kv: -kv[1]))
    path = art / "fault_risk_lgbm.txt"
    model.booster_.save_model(str(path))
    report = {
        "kind": "fault_risk_7d", "protocol": "grouped by vehicle; train days <= 12, test days >= 15; label = first DTC within 7 days, already-faulted samples dropped",
        "n_vehicles": {k: len(v) for k, v in parts.items()}, "n_train": len(tr), "n_test": len(te),
        "test_positive_rate": float(y.mean()), "params": params, "val_pr_auc": best_ap,
        "results": {"model": summarise(ml_score), "baseline_threshold_rules": summarise(rl_score)},
        "pr_auc_random_baseline": float(y.mean()), "targets": {"recall_at_precision_0.5": 0.7, "lead_time_h": 48},
        "feature_gain": imp, "artefact": {"path": path.name, "sha256": sha256(path)}, "env": env(),
    }  # fmt: skip
    (out / "fault_risk").mkdir(parents=True, exist_ok=True)
    (out / "fault_risk/report.json").write_text(json.dumps(report, indent=1))
    return report


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", type=Path, default=Path(__file__).parent / "data")
    ap.add_argument("--out", type=Path, default=Path(__file__).parent / "reports")
    ap.add_argument("--artefacts", type=Path, default=Path(__file__).parent / "artefacts")
    a = ap.parse_args()
    a.artefacts.mkdir(parents=True, exist_ok=True)
    s, f = train_soh(a.data, a.out, a.artefacts), train_fault(a.data, a.out, a.artefacts)
    (a.artefacts / "registry.json").write_text(json.dumps({"soh": s["artefact"], "fault_risk": f["artefact"]}, indent=1))
    print("SoH  MAE pp   model %.3f | linear-age %.3f | raw-coulomb %.3f  (target <= 2.0)" % (
        s["results"]["model"]["mae_pp"], s["results"]["baseline_linear_age"]["mae_pp"], s["results"]["baseline_raw_coulomb"]["mae_pp"]))  # fmt: skip
    for k in ("model", "baseline_threshold_rules"):
        r = f["results"][k]
        print(
            f"fault {k}: PR-AUC {r['pr_auc']:.3f} (random {f['pr_auc_random_baseline']:.3f}) op {r['operating_point_p>=0.5']} lead {r.get('median_lead_time_h')}"
        )


if __name__ == "__main__":
    main()
