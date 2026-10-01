import itertools
import math
import random

import numpy as np
import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

from app.domain.degradation import NO_DEGRADATION, DegradationModel
from app.domain.money import to_paise
from app.domain.schedule import Task, charge_on_arrival, evaluate, solve
from app.domain.tariff import SLOT_MS, TariffGap, TariffWindow, slot_prices

DEG = DegradationModel(0.0004, 1.0, 10.0, 0.01, 0.5, 1_000_000, 20.0)


def task(**kw: object) -> Task:
    base: dict[str, object] = {
        "vehicle_id": "v",
        "nominal_kwh": 20.0,
        "soh_pct": 100.0,
        "soc0_pct": 40.0,
        "required_pct": 70.0,
        "plug_slot": 0,
        "depart_slot": 8,
        "levels_kw": (0.0, 3.3, 7.2),
    }
    base.update(kw)
    return Task(**base)  # type: ignore[arg-type]


def brute(t: Task, price: np.ndarray, model: DegradationModel) -> float:
    """Exhaustive search with the DP's own bucket arithmetic: the DP must match its optimum."""
    inc = [math.floor(t.soc_gain_pct(p)) for p in t.levels_kw]
    best = math.inf
    n = t.depart_slot - t.plug_slot
    for combo in itertools.product(range(len(t.levels_kw)), repeat=n):
        b, cost = math.floor(t.soc0_pct), 0.0
        ok = True
        for k, i in enumerate(combo):
            p = t.levels_kw[i]
            cost += price[t.plug_slot + k] * p * 0.25
            cost += float(model.slot_cost(np.array([float(b)]), p, t.usable_kwh, t.nominal_kwh, t.temp_c, 0.25)[0])
            b += inc[i]
            if b > t.cap_pct:
                ok = False
        if ok and b >= math.ceil(t.required_pct):
            best = min(best, cost)
    return best


def dp_cost(t: Task, price: np.ndarray, model: DegradationModel) -> float:
    lv = solve(t, price, model)
    inc = [math.floor(t.soc_gain_pct(p)) for p in t.levels_kw]
    b, cost = math.floor(t.soc0_pct), 0.0
    for k in range(t.plug_slot, t.depart_slot):
        p = t.levels_kw[lv[k]]
        cost += price[k] * p * 0.25
        cost += float(model.slot_cost(np.array([float(b)]), p, t.usable_kwh, t.nominal_kwh, t.temp_c, 0.25)[0])
        b += inc[lv[k]]
    return cost


@pytest.mark.parametrize("seed", range(6))
def test_dp_matches_brute_force(seed: int) -> None:
    rng = random.Random(seed)
    t = task(soc0_pct=rng.uniform(20, 60), required_pct=rng.uniform(50, 75), depart_slot=6)
    price = np.array([rng.choice([400, 650, 900]) for _ in range(6)], dtype=float)
    expected = brute(t, price, DEG)
    assert math.isfinite(expected)
    assert dp_cost(t, price, DEG) == pytest.approx(expected, rel=1e-9)


def test_dp_charges_in_cheap_slots_and_meets_departure() -> None:
    price = np.array([900.0] * 4 + [400.0] * 4)
    t = task()
    ev = evaluate(t, solve(t, price, NO_DEGRADATION), price, NO_DEGRADATION)
    assert ev.feasible and ev.soc_pct[t.depart_slot] >= 70
    assert ev.power_kw[:4].sum() == 0  # all energy bought in the cheap half


def test_degradation_cost_prefers_lower_power_and_later_charging() -> None:
    price = np.full(8, 500.0)
    t = task(levels_kw=(0.0, 3.3, 7.2))
    ev = evaluate(t, solve(t, price, DEG), price, DEG)
    assert ev.feasible
    first = int(np.argmax(ev.power_kw > 0))
    assert first > 0  # waits at low SoC instead of charging immediately (less high-SoC dwell)
    assert ev.power_kw.max() == 3.3 or ev.power_kw[ev.power_kw > 0].min() < 7.2


def test_infeasible_is_best_effort_and_flagged() -> None:
    price = np.full(8, 500.0)
    t = task(required_pct=99.0, depart_slot=2, levels_kw=(0.0, 3.3))
    ev = evaluate(t, solve(t, price, NO_DEGRADATION), price, NO_DEGRADATION)
    assert not ev.feasible and ev.shortfall_pct > 0
    assert (ev.power_kw[:2] == 3.3).all()  # best effort = charge as much as possible


def test_soc_never_exceeds_cap_and_no_power_outside_plug_window() -> None:
    price = np.full(12, 500.0)
    t = task(soc0_pct=85.0, required_pct=95.0, plug_slot=2, depart_slot=10, cap_pct=95.0)
    ev = evaluate(t, solve(t, price, NO_DEGRADATION), price, NO_DEGRADATION)
    assert ev.soc_pct.max() <= 95.0 + 1e-9
    assert ev.power_kw[:2].sum() == 0 and ev.power_kw[10:].sum() == 0


def test_baseline_charges_on_arrival_at_max_power() -> None:
    t = task(plug_slot=1)
    lv = charge_on_arrival(t, 8)
    assert lv[0] == 0 and lv[1] == 2


@settings(max_examples=60, deadline=None)
@given(
    soc0=st.floats(5, 95),
    req=st.floats(10, 100),
    plug=st.integers(0, 10),
    stay=st.integers(1, 30),
    prices=st.lists(st.integers(100, 1500), min_size=40, max_size=40),
)
def test_dp_invariants(soc0: float, req: float, plug: int, stay: int, prices: list[int]) -> None:
    price = np.array(prices, dtype=float)
    t = task(soc0_pct=soc0, required_pct=req, plug_slot=plug, depart_slot=plug + stay)
    ev = evaluate(t, solve(t, price, DEG), price, DEG)
    assert ev.soc_pct.min() >= soc0 - 1e-9 and ev.soc_pct.max() <= 100 + 1e-9
    # If charging flat out meets the requirement, the DP must too (never trades a departure for cost).
    full = evaluate(t, charge_on_arrival(t, 40), price, DEG)
    if full.feasible and soc0 + 0 <= 100:
        floor_gain = math.floor(t.soc_gain_pct(t.levels_kw[-1])) * (min(40, t.depart_slot) - plug)
        if math.floor(soc0) + floor_gain >= math.ceil(req):
            assert ev.feasible
    # And it never costs more than the baseline when both are feasible.
    if ev.feasible and full.feasible:
        assert ev.energy_cost + ev.degradation_cost <= full.energy_cost + full.degradation_cost + 1e-6


def test_tariff_window_crossing_midnight_and_ist_alignment() -> None:
    windows = [TariffWindow(None, 1320, 360, 450), TariffWindow(None, 360, 1320, 800)]  # 22:00–06:00 off-peak
    start = 1788264000000  # 2026-09-01T12:00Z = 17:30 IST
    p = slot_prices(windows, start, 96)
    assert p[0] == 800  # 17:30 IST
    assert p[18] == 450  # 22:00 IST
    assert p[18 + 31] == 450 and p[18 + 32] == 800  # 05:45 off-peak, 06:00 peak
    with pytest.raises(ValueError):
        slot_prices(windows, start + 60_000, 4)
    assert start % SLOT_MS == 0


def test_day_specific_window_wins_and_gaps_are_errors() -> None:
    start = 1788264000000  # Tuesday 17:30 IST
    windows = [TariffWindow(1, 0, 1440, 100), TariffWindow(None, 0, 1440, 900)]
    assert slot_prices(windows, start, 1)[0] == 100
    with pytest.raises(TariffGap):
        slot_prices([TariffWindow(None, 0, 600, 1)], start, 1)


def test_money_half_even() -> None:
    assert (to_paise(2.5), to_paise(3.5), to_paise(-2.5), to_paise(10.4999)) == (2, 4, -2, 10)
