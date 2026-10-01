"""Use case: plan a depot's charging (PlanDepotCharging) and the shared rolling-horizon machinery the
back-test replays.

A vehicle's recurring duty (IST minutes) splits the timeline into depot *stays* (return → next depart).
Every stay that is in progress at the plan start or begins in the next 24 h becomes one DP task; the
horizon is 36 h so each such stay also ends inside it (shifts are ≥ 8 h). Strategy: Lagrangian plan →
greedy fallback on timeout or solver error; the uncontrolled baseline is computed with the same cost
function and validator. Money leaves this module as integer paise, converted once.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

import numpy as np

from app.domain.coordinate import Site, SitePlan, SolverTimeout, baseline, greedy, plan_site
from app.domain.degradation import DegradationModel
from app.domain.money import to_paise
from app.domain.schedule import Task
from app.domain.tariff import IST_OFFSET_MS, SLOT_MIN, SLOT_MS
from app.domain.validate import Violation, validate

log = logging.getLogger(__name__)
HORIZON_SLOTS = 144  # 36 h
NEW_STAYS_SLOTS = 96  # stays starting within the first 24 h are planned now
DEPOT_AC_KW = 22.0  # v1: every depot connector serves at most the AC level (see coordinate.py)


@dataclass(frozen=True, slots=True)
class SolverConfig:
    budget_s: float = 2.0
    max_iter: int = 8
    patience: int = 3
    levels: int = 4
    eta_ac: float = 0.92


@dataclass(frozen=True, slots=True)
class VehicleState:
    vehicle_id: str
    nominal_kwh: float
    soh_pct: float
    max_ac_kw: float
    depart_min_ist: int
    return_min_ist: int
    required_soc_pct: float
    soc_pct: float  # now (or at its next return, when it is on the road)
    at_depot: bool
    trip_soc_pct: float = 0.0  # SoC a shift uses (planned_km × consumption / usable)


@dataclass(frozen=True, slots=True)
class DepotState:
    depot_id: str
    site_cap_kw: float
    connectors: int
    vehicles: list[VehicleState]


@dataclass(frozen=True, slots=True)
class Stay:
    task: Task
    vehicle_id: str
    start_slot: int
    depart_slot: int


@dataclass(slots=True)
class PlanResult:
    depot_id: str
    start_ms: int
    method: str
    stays: list[Stay]
    plan: SitePlan
    base: SitePlan
    violations: list[Violation]
    cost_paise: int
    energy_cost_paise: int
    degradation_cost_paise: int
    baseline_cost_paise: int
    baseline_energy_cost_paise: int
    baseline_degradation_cost_paise: int
    stats: dict[str, float] = field(default_factory=dict)


def _ist_min(ms: int) -> int:
    return int(((ms + IST_OFFSET_MS) // 60_000) % 1440)


def stays_for(v: VehicleState, start_ms: int) -> list[tuple[int, int, bool]]:
    """(plug_slot, depart_slot, has_departure) of every depot stay in progress at the plan start or starting
    within NEW_STAYS_SLOTS. Walks the vehicle's daily return/depart times from the start; plug-in rounds up
    and departure rounds down to a slot (conservative). A stay still running at the horizon end gets
    has_departure=False (no SoC requirement inside this horizon). O(days)."""
    start_min = _ist_min(start_ms)
    horizon_min = HORIZON_SLOTS * SLOT_MIN
    events = sorted(
        [((v.depart_min_ist - start_min) % 1440 + 1440 * k, "D") for k in range(3)]
        + [((v.return_min_ist - start_min) % 1440 + 1440 * k, "R") for k in range(3)]
    )
    out: list[tuple[int, int, bool]] = []
    at_depot, since = v.at_depot, 0
    for t, kind in events:
        if t >= horizon_min:
            break
        if kind == "D" and at_depot:
            if t > since:
                out.append((-(-since // SLOT_MIN), t // SLOT_MIN, True))
            at_depot = False
        elif kind == "R" and not at_depot:
            at_depot, since = True, t
    if at_depot:
        out.append((-(-since // SLOT_MIN), HORIZON_SLOTS, False))
    return [(p, d, has) for p, d, has in out if p < NEW_STAYS_SLOTS and d > p]


def build_stays(depot: DepotState, start_ms: int, cfg: SolverConfig) -> list[Stay]:
    stays = []
    for v in depot.vehicles:
        top = min(v.max_ac_kw, DEPOT_AC_KW)
        levels = tuple(float(x) for x in np.round(np.linspace(0.0, top, cfg.levels), 3))
        for k, (plug, dep, has_departure) in enumerate(stays_for(v, start_ms)):
            stays.append(
                Stay(
                    task=Task(
                        vehicle_id=f"{v.vehicle_id}#{k}",
                        nominal_kwh=v.nominal_kwh,
                        soh_pct=v.soh_pct,
                        # A later stay starts after a shift that left at ≥ the required SoC: plan it from
                        # required − one shift's use (conservative; the real arrival SoC is at least that).
                        soc0_pct=v.soc_pct if k == 0 else max(5.0, v.required_soc_pct - v.trip_soc_pct),
                        required_pct=v.required_soc_pct if has_departure else 0.0,
                        plug_slot=plug,
                        depart_slot=dep,
                        levels_kw=levels,
                        eta=cfg.eta_ac,
                        cap_pct=max(100.0, v.soc_pct),
                    ),
                    vehicle_id=v.vehicle_id,
                    start_slot=plug,
                    depart_slot=dep,
                )
            )
    return stays


def plan_depot(
    depot: DepotState,
    start_ms: int,
    tariff: np.ndarray,
    model: DegradationModel,
    cfg: SolverConfig,
) -> PlanResult:
    if start_ms % SLOT_MS:
        raise ValueError("plan start must be slot-aligned")
    stays = build_stays(depot, start_ms, cfg)
    tasks = [s.task for s in stays]
    site = Site(depot.site_cap_kw, depot.connectors)
    try:
        plan = plan_site(tasks, tariff, site, model, cfg.max_iter, cfg.budget_s, patience=cfg.patience)
    except SolverTimeout:
        log.warning("depot %s: solver budget exhausted, greedy fallback", depot.depot_id)
        plan = greedy(tasks, tariff, site, model)
    except Exception:
        log.exception("depot %s: solver failed, greedy fallback", depot.depot_id)
        plan = greedy(tasks, tariff, site, model)
    if plan.method != "GREEDY_FALLBACK":
        # Safety net: the repair step is a heuristic and can leave a departure short that the simple
        # least-laxity policy meets. Both are site-legal, so keep the better one: fewest missed
        # departures first, then lowest total cost.
        alt = greedy(tasks, tariff, site, model)

        def rank(p: SitePlan) -> tuple[int, float]:
            return len(p.infeasible), p.energy_cost + p.degradation_cost

        if rank(alt) < rank(plan):
            log.info("depot %s: greedy plan beats %s (%s vs %s)", depot.depot_id, plan.method, rank(alt), rank(plan))
            alt.stats["replaced"] = 1.0
            plan = alt
    base = baseline(tasks, tariff, model)
    power = {v: e.power_kw for v, e in plan.evaluations.items()}
    violations = validate(tasks, power, site, len(tariff))
    hard = [v for v in violations if v.kind != "DEPARTURE"]
    if hard:  # the validator is the source of truth: never hand out a plan that breaks a site limit
        log.error("depot %s: %s plan failed validation (%d), greedy fallback", depot.depot_id, plan.method, len(hard))
        plan = greedy(tasks, tariff, site, model)
        power = {v: e.power_kw for v, e in plan.evaluations.items()}
        violations = validate(tasks, power, site, len(tariff))
    energy = to_paise(plan.energy_cost)
    degr = to_paise(plan.degradation_cost)
    b_energy, b_degr = to_paise(base.energy_cost), to_paise(base.degradation_cost)
    return PlanResult(
        depot_id=depot.depot_id,
        start_ms=start_ms,
        method=plan.method,
        stays=stays,
        plan=plan,
        base=base,
        violations=violations,
        cost_paise=energy + degr,
        energy_cost_paise=energy,
        degradation_cost_paise=degr,
        baseline_cost_paise=b_energy + b_degr,
        baseline_energy_cost_paise=b_energy,
        baseline_degradation_cost_paise=b_degr,
    )
