import random

import numpy as np
import pytest

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
    tasks = []
    for i in range(n):
        kwh, ac = rng.choice(MODELS)
        plug = rng.randint(0, 24)  # arrives 17:30–23:30 IST
        tasks.append(
            Task(
                vehicle_id=f"v{i:03d}",
                nominal_kwh=kwh,
                soh_pct=rng.uniform(85, 100),
                soc0_pct=rng.uniform(15, 50),
                required_pct=rng.uniform(60, 90),
                plug_slot=plug,
                depart_slot=rng.randint(plug + 30, 95),
                levels_kw=tuple(np.round(np.linspace(0, ac, 4), 2)),
            )
        )
    return tasks


def hard_kinds(tasks: list[Task], plan_power: dict[str, np.ndarray], site: Site | None) -> set[str]:
    return {v.kind for v in validate(tasks, plan_power, site, 96)} - {"DEPARTURE"}


@pytest.mark.parametrize("seed", range(4))
def test_lagrangian_plan_is_valid_and_beats_baseline(seed: int) -> None:
    tasks = depot(seed, 40)
    site = Site(cap_kw=0.35 * sum(t.levels_kw[-1] for t in tasks), connectors=20)
    plan = plan_site(tasks, TARIFF, site, DEG, budget_s=60)
    power = {v: e.power_kw for v, e in plan.evaluations.items()}
    assert hard_kinds(tasks, power, site) == set()
    assert plan.load_kw.max() <= site.cap_kw + 1e-6 and plan.active.max() <= site.connectors
    base = baseline(tasks, TARIFF, DEG)
    assert base.load_kw.max() > site.cap_kw  # uncontrolled charging breaks the cap: the problem we solve
    # Every departure the plan misses is also missed when charging flat out from arrival (truly infeasible).
    assert set(plan.infeasible) <= set(base.infeasible)
    assert plan.energy_cost + plan.degradation_cost < base.energy_cost + base.degradation_cost


def test_greedy_fallback_respects_site_limits() -> None:
    tasks = depot(9, 40)
    site = Site(cap_kw=60.0, connectors=8)
    plan = greedy(tasks, TARIFF, site, DEG)
    power = {v: e.power_kw for v, e in plan.evaluations.items()}
    assert hard_kinds(tasks, power, site) == set()
    assert plan.method == "GREEDY_FALLBACK"


def test_timeout_raises_so_caller_can_fall_back() -> None:
    calls = iter(range(10**6))
    with pytest.raises(SolverTimeout):  # the clock reads 0 when the budget starts and 10 s afterwards
        plan_site(depot(1, 5), TARIFF, Site(100.0, 5), DEG, budget_s=1.0, clock=lambda: 10.0 * min(1, next(calls)))


def test_validator_catches_each_violation() -> None:
    t = depot(2, 1)[0]
    p = np.zeros(96)
    p[t.plug_slot - 1 if t.plug_slot else 95] = t.levels_kw[-1]  # outside window
    p[t.plug_slot + 1] = 1.234  # not a level
    kinds = {v.kind for v in validate([t], {t.vehicle_id: p}, Site(cap_kw=1.0, connectors=0), 96)}
    assert {"OUTSIDE_WINDOW", "LEVEL", "DEPARTURE", "SITE_CAP", "CONNECTORS"} <= kinds
