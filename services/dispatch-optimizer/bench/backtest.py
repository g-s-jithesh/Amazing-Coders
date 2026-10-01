"""Back-test the depot planner against charge-on-arrival on seed depots.

uv run python -m bench.backtest --depots 5 --days 30 > out.json   (run `make seed` first)
Prints one JSON document: per-depot ledgers, totals, and the run environment.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import statistics
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from app.application.backtest import Ledger, run_backtest
from app.domain.money import to_paise
from app.domain.tariff import slot_prices
from app.infrastructure.files import load_degradation, load_depots, load_solver, load_tariff

ROOT = Path(__file__).resolve().parents[3]
SVC = Path(__file__).resolve().parents[1]


def ledger(lg: Ledger) -> dict[str, float | int]:
    return {
        "energy_kwh": round(lg.energy_kwh, 1),
        "energy_cost_paise": to_paise(lg.energy_cost),
        "degradation_cost_paise": to_paise(lg.degradation_cost),
        "total_cost_paise": to_paise(lg.energy_cost) + to_paise(lg.degradation_cost),
        "departures": lg.departures,
        "missed_departures": lg.missed_departures,
        "peak_kw": round(lg.peak_kw, 1),
        "slots_over_cap": lg.slots_over_cap,
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--depots", type=int, default=5)
    ap.add_argument("--days", type=int, default=30)
    ap.add_argument("--start", default="2026-09-01T00:00:00+05:30", help="IST midnight")
    ap.add_argument("--tariff", default="DEPOT_TOD_SYNTH")
    ap.add_argument("--seed-dir", default=str(ROOT / "data" / "seed"))
    a = ap.parse_args()

    model = load_degradation(SVC / "config" / "degradation.toml")
    cfg = load_solver(SVC / "config" / "solver.toml")
    windows = load_tariff(ROOT / "data" / "reference" / "tariffs.csv", a.tariff)
    start_ms = int(datetime.fromisoformat(a.start).timestamp() * 1000)
    depots = load_depots(Path(a.seed_dir), limit=a.depots)

    out: list[dict[str, Any]] = []
    t0 = time.perf_counter()
    for d in depots:
        r = run_backtest(d, start_ms, a.days, lambda ms, n: slot_prices(windows, ms, n), model, cfg)
        opt, base = ledger(r.optimised), ledger(r.baseline)
        out.append(
            {
                "depot_id": d.depot_id,
                "vehicles": len(d.vehicles),
                "site_cap_kw": d.site_cap_kw,
                "connectors": d.connectors,
                "optimised": opt,
                "baseline": base,
                "methods": r.methods,
                "plan_s_median": round(statistics.median(r.plan_seconds), 3),
                "plan_s_max": round(max(r.plan_seconds), 3),
            }
        )
        print(json.dumps({"progress": d.depot_id, "elapsed_s": round(time.perf_counter() - t0, 1)}), flush=True)

    def tot(key: str, side: str) -> int:
        return sum(int(o[side][key]) for o in out)

    totals: dict[str, Any] = {
        side: {
            k: tot(k, side)
            for k in (
                "energy_cost_paise",
                "degradation_cost_paise",
                "total_cost_paise",
                "departures",
                "missed_departures",
                "slots_over_cap",
            )
        }
        for side in ("optimised", "baseline")
    }
    b, o = totals["baseline"], totals["optimised"]
    totals["saving_total_pct"] = round(100 * (b["total_cost_paise"] - o["total_cost_paise"]) / b["total_cost_paise"], 2)
    totals["saving_energy_pct"] = round(
        100 * (b["energy_cost_paise"] - o["energy_cost_paise"]) / b["energy_cost_paise"], 2
    )
    print(
        json.dumps(
            {
                "kind": "dispatch_backtest",
                "args": vars(a),
                "depots": out,
                "totals": totals,
                "wall_s": round(time.perf_counter() - t0, 1),
                "env": {
                    "python": platform.python_version(),
                    "machine": platform.machine(),
                    "processor": platform.processor(),
                    "cpus": os.cpu_count(),
                    "at": datetime.now(UTC).isoformat(timespec="seconds"),
                },
            },
            indent=1,
        )
    )


if __name__ == "__main__":
    main()
