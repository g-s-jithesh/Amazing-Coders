from pathlib import Path

import numpy as np

from app.application.backtest import DepotMaster, VehicleMaster, run_backtest
from app.application.planning import DepotState, SolverConfig, VehicleState, plan_depot, stays_for
from app.domain.degradation import DegradationModel
from app.domain.tariff import TariffWindow, slot_prices
from app.infrastructure.files import load_degradation, load_solver, load_tariff

SVC = Path(__file__).resolve().parents[1]
ROOT = SVC.parents[1]
MIDNIGHT_IST = 1788201000000  # 2026-09-01T00:00+05:30
DEG = DegradationModel(0.0003, 1.0, 10.0, 0.01, 0.5, 1_000_000, 20.0)
WINDOWS = [TariffWindow(None, 1320, 360, 385), TariffWindow(None, 360, 1080, 485), TariffWindow(None, 1080, 1320, 600)]


def vehicle(dep: int, ret: int, at_depot: bool, soc: float = 50.0) -> VehicleState:
    return VehicleState("v", 45.0, 100.0, 11.0, dep, ret, 80.0, soc, at_depot, 20.0)


def test_stays_morning_shift_from_midnight() -> None:
    # at the depot since 14:00 yesterday; leaves 06:00 (slot 24), back 14:00 (56), leaves 06:00 (120)
    assert stays_for(vehicle(360, 840, True), MIDNIGHT_IST) == [(0, 24, True), (56, 120, True)]


def test_stays_night_shift_away_at_midnight() -> None:
    # on the road at midnight; back 06:00 (24), leaves 22:00 (88); the next return (06:00, slot 120) is
    # beyond the 24 h window for new stays
    assert stays_for(vehicle(1320, 360, False), MIDNIGHT_IST) == [(24, 88, True)]


def test_stay_past_the_horizon_has_no_departure_requirement() -> None:
    start = MIDNIGHT_IST + (22 * 60 + 15) * 60_000  # 22:15 IST, evening shift (14:00–22:00) just back
    out = stays_for(vehicle(840, 1320, True), start)
    assert out[0] == (0, 63, True)
    assert out[-1] == (95, 144, False)


def test_plan_depot_valid_and_falls_back_on_timeout() -> None:
    depot = DepotState(
        "d",
        site_cap_kw=30.0,
        connectors=3,
        vehicles=[VehicleState(f"v{i}", 45.0, 95.0, 11.0, 360, 840, 80.0, 40.0 + i, True, 30.0) for i in range(6)],
    )
    tariff = slot_prices(WINDOWS, MIDNIGHT_IST, 144)
    res = plan_depot(depot, MIDNIGHT_IST, tariff, DEG, SolverConfig())
    assert res.method.startswith("LAGRANGIAN")
    assert not [v for v in res.violations if v.kind != "DEPARTURE"]
    assert res.cost_paise == res.energy_cost_paise + res.degradation_cost_paise
    assert res.plan.load_kw.max() <= 30.0 + 1e-6 and res.plan.active.max() <= 3
    slow = plan_depot(depot, MIDNIGHT_IST, tariff, DEG, SolverConfig(budget_s=-1.0))
    assert slow.method == "GREEDY_FALLBACK"
    assert not [v for v in slow.violations if v.kind != "DEPARTURE"]


def test_backtest_meets_departures_and_shifts_energy_to_cheap_hours() -> None:
    vs = [VehicleMaster(f"v{i}", 45.0, 100.0, 11.0, 540, 1080, 80.0, 15.0) for i in range(4)]  # general shift
    depot = DepotMaster("d", site_cap_kw=200.0, connectors=4, vehicles=vs)
    r = run_backtest(depot, MIDNIGHT_IST, 2, lambda ms, n: slot_prices(WINDOWS, ms, n), DEG, SolverConfig())
    assert r.optimised.missed_departures == 0 and r.baseline.missed_departures == 0
    assert r.optimised.departures == 8
    # back at 18:00 (peak): the baseline pays the peak rate, the plan waits for the night rate
    assert r.optimised.energy_cost < 0.8 * r.baseline.energy_cost


def test_config_files_load() -> None:
    model = load_degradation(SVC / "config" / "degradation.toml")
    cfg = load_solver(SVC / "config" / "solver.toml")
    w = load_tariff(ROOT / "data" / "reference" / "tariffs.csv", "DEPOT_TOD_SYNTH")
    assert model.usable_soh_range_pp == 20.0 and cfg.budget_s > 0
    p = slot_prices(w, MIDNIGHT_IST, 96)
    assert set(np.unique(p)) == {385.0, 485.0, 600.0}


def test_never_misses_a_departure_the_greedy_policy_meets() -> None:
    # Tight connectors: the Lagrangian repair alone left two vehicles short here; the safety net keeps
    # the plan with fewer missed departures.
    vs = [VehicleState(f"v{i}", 45.0, 95.0, 11.0, 540, 1080, 80.0, 30.0, True, 30.0) for i in range(5)]
    depot = DepotState("d", site_cap_kw=25.0, connectors=3, vehicles=vs)
    res = plan_depot(depot, MIDNIGHT_IST, slot_prices(WINDOWS, MIDNIGHT_IST, 144), DEG, SolverConfig())
    assert res.plan.infeasible == []
