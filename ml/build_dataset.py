"""Build the training tables from simulator traces. Runs locally (needs the simulator binary and data/seed);
the resulting Parquet files are small, synthetic, and committed so Colab only needs `git clone`.

    go build -o sim-trace ./services/simulator/cmd/simulator   (from services/simulator)
    uv run python build_dataset.py --sim <path-to-binary> --vehicles 1200

This is the only place that reads the simulator's truth column (`soh_true`), as labels. Features use
observable columns only; `leakage_check` asserts that. Protocol (stated again in the reports):

* SoH: one row per vehicle from its first 7 days. Training labels simulate workshop capacity tests
  (a random 30 % of the training vehicles, +-0.5 pp noise); evaluation uses the exact truth for every
  test vehicle. Vehicle age comes from master data (commissioned_on).
* Fault risk: one row per vehicle per day (day 3..21), features from the 72 h before that moment, label =
  first DTC within the next 7 days, with samples already past their first DTC dropped.
"""

from __future__ import annotations

import argparse
import csv
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor
from datetime import date
from io import StringIO
from pathlib import Path

import numpy as np
import pandas as pd

ROOT = Path(__file__).resolve().parents[1]
DAYS = 28
DT_S, EVERY = 10, 30  # 5-minute rows
ROW_S = DT_S * EVERY
ROWS_PER_DAY = 86400 // ROW_S
START = date(2026, 9, 1)

# Columns a feature may use. `soh_true` is the label source and must never appear here.
OBSERVABLE = {
    "mode", "speed_kmh", "odo_km", "soc_pct", "pack_voltage_v", "pack_current_a", "pack_temp_max_c",
    "cell_v_min_mv", "cell_v_max_mv", "isolation_kohm", "hv_interlock_ok", "aux_12v_v", "ambient_c",
    "charge_power_kw", "dtc",
}  # fmt: skip


def run_trace(sim: Path, vin: str, seed: int, vehicles_total: int, fault_rate: float) -> pd.DataFrame:
    out = subprocess.run(
        [
            str(sim), "trace", "--seed", str(seed), "--vehicles", str(vehicles_total), "--ref", str(ROOT / "data/reference"),
            "--vin", vin, "--hours", str(DAYS * 24), "--dt", str(DT_S), "--every", str(EVERY),
            "--fault-rate", str(fault_rate),
        ],
        capture_output=True, text=True, check=True,
    )  # fmt: skip
    return pd.read_csv(StringIO(out.stdout), keep_default_na=False, na_values=[""])


def slope_per_h(y: np.ndarray) -> float:
    x = np.arange(len(y)) * ROW_S / 3600.0
    if len(y) < 3 or np.isnan(y).all():
        return float("nan")
    return float(np.polyfit(x, y, 1)[0])


def window_features(w: pd.DataFrame, w24: pd.DataFrame) -> dict[str, float]:
    td = (w["pack_temp_max_c"] - w["ambient_c"]).to_numpy()
    td24 = (w24["pack_temp_max_c"] - w24["ambient_c"]).to_numpy()
    imb = (w["cell_v_max_mv"] - w["cell_v_min_mv"]).to_numpy()
    imb24 = (w24["cell_v_max_mv"] - w24["cell_v_min_mv"]).to_numpy()
    iso = w["isolation_kohm"].to_numpy()
    ok = w["hv_interlock_ok"].astype(str).str.lower().eq("true").to_numpy()
    return {
        "td_mean": float(td.mean()), "td_max": float(td.max()), "td_slope_per_h": slope_per_h(td),
        "td_mean_24h": float(td24.mean()),
        "imb_mean": float(imb.mean()), "imb_max": float(imb.max()), "imb_slope_per_h": slope_per_h(imb),
        "imb_mean_24h": float(imb24.mean()),
        "iso_min": float(iso.min()), "iso_mean": float(iso.mean()), "iso_slope_per_h": slope_per_h(iso),
        "iso_min_24h": float(w24["isolation_kohm"].min()),
        "hvil_false_frac": float((~ok).mean()), "hvil_flaps": float((ok[1:] != ok[:-1]).sum()),
        "aux_min": float(w["aux_12v_v"].min()), "aux_mean": float(w["aux_12v_v"].mean()),
        "soc_mean": float(w["soc_pct"].mean()), "soc_gt80_frac": float((w["soc_pct"] > 80).mean()),
    }  # fmt: skip


def fault_rows(df: pd.DataFrame, vin: str) -> list[dict[str, object]]:
    dtc = df["dtc"].notna().to_numpy()
    first = int(np.argmax(dtc)) if dtc.any() else None
    rows = []
    for day in range(3, 22):
        end = day * ROWS_PER_DAY
        if first is not None and first < end:
            continue  # already faulted: nothing left to predict
        w, w24 = df.iloc[end - 3 * ROWS_PER_DAY : end], df.iloc[end - ROWS_PER_DAY : end]
        horizon_end = end + 7 * ROWS_PER_DAY
        label = int(first is not None and end <= first < horizon_end)
        lead_h = (first - end) * ROW_S / 3600.0 if label else float("nan")
        rows.append({"vin": vin, "day": day, **window_features(w, w24), "label": label, "lead_h": lead_h})
    return rows


def coulomb_soh(df: pd.DataFrame, cap_ah: float) -> float:
    """Median coulomb-counting SoH over charging segments with dSoC >= 20 pp (observable data only)."""
    charging = df["mode"].eq("CHARGING").to_numpy()
    soc, cur = df["soc_pct"].to_numpy(), df["pack_current_a"].to_numpy()
    vals, i = [], 0
    while i < len(df):
        if not charging[i]:
            i += 1
            continue
        j = i
        while j + 1 < len(df) and charging[j + 1]:
            j += 1
        d_soc = soc[j] - soc[i]
        if d_soc >= 20:
            ah = np.abs(cur[i : j + 1]).sum() * ROW_S / 3600.0
            vals.append(ah / (d_soc / 100.0) / cap_ah * 100.0)
        i = j + 1
    return float(np.median(vals)) if vals else float("nan")


def soh_row(df: pd.DataFrame, vin: str, meta: dict[str, str], cap_ah: float) -> dict[str, object]:
    wk = df.iloc[: 7 * ROWS_PER_DAY]
    cur = wk["pack_current_a"].to_numpy()
    age = (START - date.fromisoformat(meta["commissioned_on"])).days
    return {
        "vin": vin, "model_code": meta["model_code"], "age_days": age, "capacity_kwh": float(meta["capacity_kwh"]),
        "efc_week": float(np.abs(cur).sum() * ROW_S / 3600.0 / (2 * cap_ah)),
        "temp_mean": float(wk["pack_temp_max_c"].mean()), "ambient_mean": float(wk["ambient_c"].mean()),
        "soc_gt80_frac": float((wk["soc_pct"] > 80).mean()), "charge_kw_mean": float(wk["charge_power_kw"].mean()),
        "coulomb_soh": coulomb_soh(wk, cap_ah),
        "soh_true_pct": float(df["soh_true"].iloc[7 * ROWS_PER_DAY - 1] * 100.0),  # label source
    }  # fmt: skip


def leakage_check(df: pd.DataFrame) -> None:
    assert "soh_true" not in OBSERVABLE
    bad = [c for c in df.columns if c not in OBSERVABLE | {"soh_true", "ts_ist", "evt", "lat", "lon"}]
    assert not bad, f"unexpected trace columns: {bad}"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--sim", required=True, type=Path)
    ap.add_argument("--vehicles", type=int, default=1200)
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--fleet", type=int, default=100_000, help="fleet size the master data was generated with")
    ap.add_argument("--fault-rate", type=float, default=8.0, help="random fault onsets per vehicle-year")
    ap.add_argument("--workers", type=int, default=8)
    ap.add_argument("--out", type=Path, default=Path(__file__).parent / "data")
    a = ap.parse_args()

    with (ROOT / "data/seed/vehicle.csv").open(newline="", encoding="utf-8") as f:
        veh = list(csv.DictReader(f))
    models = {r["code"]: r for r in csv.DictReader((ROOT / "data/seed/vehicle_model.csv").open(newline="", encoding="utf-8"))}
    rng = np.random.default_rng(a.seed)
    pick = rng.choice(len(veh), size=a.vehicles, replace=False)
    chosen = [veh[i] for i in sorted(pick)]

    def one(v: dict[str, str]) -> tuple[list[dict[str, object]], dict[str, object]]:
        df = run_trace(a.sim, v["vin"], a.seed, a.fleet, a.fault_rate)
        leakage_check(df)
        m = models[v["model_code"]]
        cap_ah = float(m["nominal_capacity_kwh"]) * 1000 / float(m["nominal_voltage_v"])
        meta = {"model_code": v["model_code"], "capacity_kwh": m["nominal_capacity_kwh"], "commissioned_on": v["commissioned_on"]}
        return fault_rows(df, v["vin"]), soh_row(df, v["vin"], meta, cap_ah)

    faults: list[dict[str, object]] = []
    sohs: list[dict[str, object]] = []
    with ThreadPoolExecutor(a.workers) as ex:
        for k, (fr, sr) in enumerate(ex.map(one, chosen), 1):
            faults += fr
            sohs.append(sr)
            if k % 100 == 0:
                print(f"{k}/{len(chosen)} vehicles", file=sys.stderr, flush=True)

    a.out.mkdir(parents=True, exist_ok=True)
    pd.DataFrame(sohs).to_parquet(a.out / "soh.parquet", index=False)
    pd.DataFrame(faults).to_parquet(a.out / "fault_risk.parquet", index=False)
    f = pd.DataFrame(faults)
    print(f"soh rows {len(sohs)}; fault rows {len(f)}, positives {int(f['label'].sum())} ({f['label'].mean():.1%})")


if __name__ == "__main__":
    main()
