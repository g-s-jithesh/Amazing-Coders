"""Depot solve time and plan quality vs depot size and Lagrangian iterations (synthetic random depots).

uv run python -m bench.bench_depot [sizes] [iters]   → one JSON line per run on stdout
"""

from __future__ import annotations

import json
import platform
import random
import sys
import time

import numpy as np

from app.domain.coordinate import Site, SolverTimeout, baseline, greedy, plan_site
from app.domain.degradation import DegradationModel
from app.domain.schedule import Task
from app.domain.tariff import TariffWindow, slot_prices
from app.domain.validate import validate

DEG = DegradationModel(0.0004, 1.0, 10.0, 0.01, 0.5, 1_000_000, 20.0)
START = 1788264000000  # 17:30 IST
TARIFF = slot_prices([TariffWindow(None, 1320, 360, 450), TariffWindow(None, 360, 1320, 850)], START, 96)
MODELS = [(21, 3.3), (30, 7.2), (45, 11.0), (70, 22.0)]


def depot(seed: int, n: int) -> list[Task]:
    rng = random.Random(seed)
    out = []
    for i in range(n):
        kwh, ac = rng.choice(MODELS)
        plug = rng.randint(0, 24)
        out.append(
            Task(
                f"v{i:04d}",
                kwh,
                rng.uniform(85, 100),
                rng.uniform(15, 50),
                rng.uniform(60, 90),
                plug,
                rng.randint(plug + 30, 95),
                tuple(np.round(np.linspace(0, ac, 4), 2)),
            )
        )
    return out


def main() -> None:
    sizes = [int(x) for x in (sys.argv[1] if len(sys.argv) > 1 else "50,250,1000").split(",")]
    iters = [int(x) for x in (sys.argv[2] if len(sys.argv) > 2 else "5,15,40").split(",")]
    for n in sizes:
        tasks = depot(n, n)
        # Site sized like the seed data: cap ≈ 0.35 × Σ vehicle max kW, 0.5 connectors per vehicle.
        site = Site(cap_kw=0.35 * sum(t.levels_kw[-1] for t in tasks), connectors=n // 2)
        base = baseline(tasks, TARIFF, DEG)
        g0 = time.perf_counter()
        g = greedy(tasks, TARIFF, site, DEG)
        g_s = time.perf_counter() - g0
        for it in iters:
            t0 = time.perf_counter()
            try:
                plan = plan_site(tasks, TARIFF, site, DEG, max_iter=it, budget_s=600)
            except SolverTimeout:
                continue
            secs = time.perf_counter() - t0
            power = {v: e.power_kw for v, e in plan.evaluations.items()}
            hard = [v.kind for v in validate(tasks, power, site, 96) if v.kind != "DEPARTURE"]
            print(
                json.dumps(
                    {
                        "vehicles": n,
                        "max_iter": it,
                        "method": plan.method,
                        "solve_s": round(secs, 3),
                        "repairs": plan.repairs,
                        "hard_violations": len(hard),
                        "missed_departures": len(plan.infeasible),
                        "missed_departures_baseline": len(base.infeasible),
                        "missed_departures_greedy": len(g.infeasible),
                        "cost_paise": round(plan.energy_cost + plan.degradation_cost),
                        "cost_paise_baseline": round(base.energy_cost + base.degradation_cost),
                        "cost_paise_greedy": round(g.energy_cost + g.degradation_cost),
                        "greedy_s": round(g_s, 3),
                        "peak_kw": round(float(plan.load_kw.max()), 1),
                        "peak_kw_baseline": round(float(base.load_kw.max()), 1),
                        "cap_kw": round(site.cap_kw, 1),
                        "python": platform.python_version(),
                        "machine": platform.machine(),
                        "processor": platform.processor(),
                    }
                ),
                flush=True,
            )


if __name__ == "__main__":
    main()
