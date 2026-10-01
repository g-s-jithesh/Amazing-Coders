"""Use case: BacktestPlans. Replays N days of a depot with daily rolling replanning and executes the plan
against the actual SoC, next to the uncontrolled baseline (charge on arrival at max power, ignoring the
site cap) run through the same execution and cost code.

Execution model (deterministic, from master data only, no simulator ground truth): a vehicle at the depot
receives min(requested power, what still fits below 100 %); at each departure the SoC is checked against
the required SoC (a miss is counted) and one shift's energy (planned_km × consumption) is removed. Day 0 is
a warm-up and is excluded from the totals. O(days · (plan + N·T)).
"""

from __future__ import annotations

import time
from collections.abc import Callable
from dataclasses import dataclass, field

import numpy as np

from app.application.planning import DepotState, PlanResult, SolverConfig, VehicleState, plan_depot
from app.domain.degradation import DegradationModel
from app.domain.schedule import DT_H, Floats

DAY_MS = 86_400_000
DAY_SLOTS = 96


@dataclass(frozen=True, slots=True)
class VehicleMaster:
    vehicle_id: str
    nominal_kwh: float
    soh_pct: float
    max_ac_kw: float
    depart_min_ist: int
    return_min_ist: int
    required_soc_pct: float
    trip_kwh: float


@dataclass(frozen=True, slots=True)
class DepotMaster:
    depot_id: str
    site_cap_kw: float
    connectors: int
    vehicles: list[VehicleMaster]
    tenant_id: str = ""


@dataclass(slots=True)
class Ledger:
    energy_kwh: float = 0.0
    energy_cost: float = 0.0  # paise (float until reported)
    degradation_cost: float = 0.0
    missed_departures: int = 0
    departures: int = 0
    peak_kw: float = 0.0
    slots_over_cap: int = 0


@dataclass(slots=True)
class BacktestResult:
    depot_id: str
    days: int
    optimised: Ledger = field(default_factory=Ledger)
    baseline: Ledger = field(default_factory=Ledger)
    methods: dict[str, int] = field(default_factory=dict)
    plan_seconds: list[float] = field(default_factory=list)


def _usable(v: VehicleMaster) -> float:
    return v.nominal_kwh * v.soh_pct / 100.0


def at_depot(v: VehicleMaster, ist_min: int) -> bool:
    d, r = v.depart_min_ist, v.return_min_ist
    in_shift = (d <= ist_min < r) if d < r else (ist_min >= d or ist_min < r)
    return not in_shift


def _execute_day(
    depot: DepotMaster,
    soc: dict[str, float],
    start_ms: int,
    tariff: Floats,
    requested: Callable[[str, int], float],
    model: DegradationModel,
    ledger: Ledger,
    count: bool,
    eta: float,
    fcfs_connectors: int | None = None,
) -> None:
    """Advance every vehicle's SoC through one day, slot by slot. With ``fcfs_connectors`` (the baseline),
    at most that many vehicles charge per slot, first come first served (a full vehicle frees its
    connector); an optimised plan already respects the connector count."""
    load = np.zeros(DAY_SLOTS)
    start_min = int(((start_ms + 330 * 60_000) // 60_000) % 1440)
    arrived = {v.vehicle_id: -1 for v in depot.vehicles}
    for k in range(DAY_SLOTS):
        ist = (start_min + 15 * k) % 1440
        here = []
        for v in depot.vehicles:
            vid, usable = v.vehicle_id, _usable(v)
            if ist == v.depart_min_ist:  # departure at the start of this slot
                if count:
                    ledger.departures += 1
                    ledger.missed_departures += int(soc[vid] < v.required_soc_pct - 1e-6)
                soc[vid] = max(0.0, soc[vid] - v.trip_kwh / usable * 100.0)
            if ist == v.return_min_ist:
                arrived[vid] = k
            if at_depot(v, ist) and soc[vid] < 100.0 - 1e-9 and requested(vid, k) > 0:
                here.append(v)
        if fcfs_connectors is not None:
            here = sorted(here, key=lambda x: (arrived[x.vehicle_id], x.vehicle_id))[:fcfs_connectors]
        for v in here:
            vid, usable = v.vehicle_id, _usable(v)
            fits = max(0.0, (100.0 - soc[vid]) * usable / (eta * DT_H * 100.0))
            p = min(requested(vid, k), fits)
            if count:
                ledger.energy_kwh += p * DT_H
                ledger.energy_cost += float(tariff[k]) * p * DT_H
                ledger.degradation_cost += float(
                    model.slot_cost(np.array([soc[vid]]), p, usable, v.nominal_kwh, 25.0, DT_H)[0]
                )
            load[k] += p
            soc[vid] = min(100.0, soc[vid] + eta * p * DT_H / usable * 100.0)
    if count:
        ledger.peak_kw = max(ledger.peak_kw, float(load.max()))
        ledger.slots_over_cap += int((load > depot.site_cap_kw + 1e-6).sum())


def run_backtest(
    depot: DepotMaster,
    start_ms: int,
    days: int,
    tariff_for: Callable[[int, int], Floats],
    model: DegradationModel,
    cfg: SolverConfig,
    initial_soc_pct: float = 60.0,
    clock: Callable[[], float] | None = None,
) -> BacktestResult:
    """tariff_for(start_ms, slots) → slot prices. start_ms must be slot-aligned."""
    tick = clock or time.perf_counter
    res = BacktestResult(depot.depot_id, days)
    soc_opt = {v.vehicle_id: initial_soc_pct for v in depot.vehicles}
    soc_base = dict(soc_opt)
    top = {v.vehicle_id: min(v.max_ac_kw, 22.0) for v in depot.vehicles}
    for day in range(days + 1):  # day 0 = warm-up
        day_ms = start_ms + day * DAY_MS
        count = day > 0
        start_min = int(((day_ms + 330 * 60_000) // 60_000) % 1440)
        state = DepotState(
            depot.depot_id,
            depot.site_cap_kw,
            depot.connectors,
            [
                VehicleState(
                    v.vehicle_id,
                    v.nominal_kwh,
                    v.soh_pct,
                    v.max_ac_kw,
                    v.depart_min_ist,
                    v.return_min_ist,
                    v.required_soc_pct,
                    # away: SoC at return = SoC now (the shift's use was removed at departure)
                    soc_opt[v.vehicle_id],
                    at_depot(v, start_min),
                    v.trip_kwh / _usable(v) * 100.0,
                )
                for v in depot.vehicles
            ],
        )
        t0 = tick()
        plan: PlanResult = plan_depot(state, day_ms, tariff_for(day_ms, 144), model, cfg)
        if count:
            res.plan_seconds.append(tick() - t0)
            res.methods[plan.method] = res.methods.get(plan.method, 0) + 1
        power: dict[str, Floats] = {}
        for st in plan.stays:
            p = plan.plan.evaluations[st.task.vehicle_id].power_kw
            power.setdefault(st.vehicle_id, np.zeros(144))
            power[st.vehicle_id] = power[st.vehicle_id] + p
        tariff_day = tariff_for(day_ms, DAY_SLOTS)

        def planned(vid: str, k: int, power: dict[str, Floats] = power) -> float:
            return float(power[vid][k]) if vid in power else 0.0

        _execute_day(
            depot,
            soc_opt,
            day_ms,
            tariff_day,
            planned,
            model,
            res.optimised,
            count,
            cfg.eta_ac,
        )
        _execute_day(
            depot,
            soc_base,
            day_ms,
            tariff_day,
            lambda vid, k: top[vid],
            model,
            res.baseline,
            count,
            cfg.eta_ac,
            fcfs_connectors=depot.connectors,
        )
    return res
