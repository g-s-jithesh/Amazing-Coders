"""Depot (site) coordination: per-vehicle DPs coupled by two per-slot site constraints.

    Σ_v P_v[t] ≤ site_cap_kw              (price λ[t], paise/kWh)
    #{v : P_v[t] > 0} ≤ connectors        (price μ[t], paise per vehicle-slot)

Lagrangian relaxation: each vehicle's DP sees tariff + λ and pays μ when it draws power; projected
subgradient updates raise the prices of overloaded slots until both constraints hold. If they still do
not hold when the iteration budget ends, a repair step lowers the power cap of the vehicle with the most
departure slack in the worst slot and re-solves only that vehicle, until every slot is legal. Strategy
alternatives: ``greedy`` (least-laxity first, used on timeout) and ``charge_on_arrival`` (baseline).

v1 assumption: connectors are a pool of AC-capable points; a vehicle may be moved to a free connector at
a slot boundary (depot staff re-plug), so only the count per slot matters. Power is limited by the
vehicle's AC maximum. ponytail: no per-connector power, no re-plug cost; model both when depot ops ask.

O(iter · N · T·S·P) + O(repairs · T·S·P).
"""

from __future__ import annotations

import math
import time
from collections.abc import Callable
from dataclasses import dataclass, field

import numpy as np

from app.domain.degradation import DegradationModel
from app.domain.schedule import DT_H, Evaluation, Floats, Ints, Task, charge_on_arrival, evaluate, solve_batch

EPS = 1e-6


class SolverTimeout(Exception):
    pass


@dataclass(frozen=True, slots=True)
class Site:
    cap_kw: float
    connectors: int


@dataclass(slots=True)
class SitePlan:
    method: str  # LAGRANGIAN | LAGRANGIAN_REPAIRED | GREEDY_FALLBACK | CHARGE_ON_ARRIVAL
    levels: dict[str, Ints]
    evaluations: dict[str, Evaluation]
    load_kw: Floats
    active: Ints  # vehicles drawing power per slot
    iterations: int = 0
    repairs: int = 0
    stats: dict[str, float] = field(default_factory=dict)

    @property
    def energy_cost(self) -> float:
        return sum(e.energy_cost for e in self.evaluations.values())

    @property
    def degradation_cost(self) -> float:
        return sum(e.degradation_cost for e in self.evaluations.values())

    @property
    def infeasible(self) -> list[str]:
        return sorted(v for v, e in self.evaluations.items() if not e.feasible)


def _finish(
    method: str, tasks: list[Task], levels: dict[str, Ints], tariff: Floats, model: DegradationModel
) -> SitePlan:
    evals = {t.vehicle_id: evaluate(t, levels[t.vehicle_id], tariff, model) for t in tasks}
    load = np.sum([e.power_kw for e in evals.values()], axis=0) if evals else np.zeros(len(tariff))
    active = (
        np.sum([e.power_kw > 0 for e in evals.values()], axis=0).astype(np.int64)
        if evals
        else np.zeros(len(tariff), dtype=np.int64)
    )
    return SitePlan(method, levels, evals, load, active)


def _load(tasks: list[Task], levels: dict[str, Ints], t_len: int) -> tuple[Floats, Ints]:
    load = np.zeros(t_len)
    active = np.zeros(t_len, dtype=np.int64)
    for t in tasks:
        p = np.asarray(t.levels_kw)[levels[t.vehicle_id]]
        load += p
        active += p > 0
    return load, active


def _departure_soc(t: Task, lv: Ints) -> float:
    """SoC at departure without taper (min with the cap): cheap slack estimate for the repair ranking."""
    gain = float(np.asarray(t.levels_kw)[lv[t.plug_slot : t.depart_slot]].sum()) * t.soc_gain_pct(1.0)
    return min(t.cap_pct, t.soc0_pct + gain)


def plan_site(
    tasks: list[Task],
    tariff: Floats,
    site: Site,
    model: DegradationModel,
    max_iter: int = 8,
    budget_s: float = 2.0,
    patience: int = 3,
    clock: Callable[[], float] = time.monotonic,
) -> SitePlan:
    """Lagrangian plan; raises SolverTimeout when the time budget is exhausted (caller falls back)."""
    deadline = clock() + budget_s
    t_len = len(tariff)
    lam = np.zeros(t_len)  # paise/kWh
    mu = np.zeros(t_len)  # paise per vehicle-slot
    spread = float(tariff.max() - tariff.min()) + 0.1 * float(tariff.mean()) + 1.0
    typical_kw = max(1e-9, float(np.mean([t.levels_kw[-1] for t in tasks]))) if tasks else 1.0
    levels: dict[str, Ints] = {}
    top_kw = np.array([max(t.levels_kw[-1], EPS) for t in tasks])
    # Deterministic dither (±0.1 % of the mean price) breaks ties between identical vehicles, which would
    # otherwise all jump to the same cheapest slot every iteration and keep the subgradient oscillating.
    dither = np.random.default_rng(0).uniform(-1e-3, 1e-3, (len(tasks), t_len)) * float(tariff.mean())
    best_levels: dict[str, Ints] = {}
    best_prices = (lam, mu)
    best_it = 0
    best_violation = math.inf
    it = 0
    for it in range(1, max_iter + 1):
        if clock() > deadline:
            raise SolverTimeout
        conn_price = mu[None, :] / (top_kw[:, None] * DT_H)  # μ per vehicle-slot as a per-kWh price
        batch = solve_batch(tasks, tariff[None, :] + lam[None, :] + conn_price + dither, model)
        levels = {t.vehicle_id: batch[j] for j, t in enumerate(tasks)}
        load, active = _load(tasks, levels, t_len)
        over_kw, over_n = load - site.cap_kw, active - site.connectors
        violation = float(np.maximum(over_kw, 0).sum() / max(site.cap_kw, EPS) + np.maximum(over_n, 0).sum())
        if violation < best_violation:
            best_violation, best_levels, best_prices = violation, levels, (lam.copy(), mu.copy())
            best_it = it
        elif it - best_it >= patience:
            break  # measured: once the violation stops improving, more iterations do not change the plan
        if (over_kw <= EPS).all() and (over_n <= 0).all():
            plan = _finish("LAGRANGIAN", tasks, levels, tariff, model)
            plan.iterations = it
            return plan
        step = spread / math.sqrt(it)
        lam = np.maximum(0.0, lam + step * over_kw / max(site.cap_kw, EPS))
        mu = np.maximum(0.0, mu + step * typical_kw * DT_H * over_n / max(site.connectors, 1))

    # Repair from the least-violating iterate, in rounds: in every overloaded slot, cap the vehicles with
    # the most departure slack (one power level for kW excess, to zero for connector excess) until the
    # excess is covered, then re-solve all capped vehicles in one batch. Caps only ever decrease, so this
    # terminates; re-solving can move load into other slots, which the next round handles.
    levels = best_levels
    lam, mu = best_prices
    index = {t.vehicle_id: j for j, t in enumerate(tasks)}
    caps = np.array([[len(t.levels_kw) - 1] * t_len for t in tasks], dtype=np.int64)
    price = tariff[None, :] + lam[None, :] + mu[None, :] / (top_kw[:, None] * DT_H) + dither
    repairs = rounds = 0
    while True:
        if clock() > deadline:
            raise SolverTimeout
        load, active = _load(tasks, levels, t_len)
        bad = np.nonzero((load - site.cap_kw > EPS) | (active > site.connectors))[0]
        if len(bad) == 0:
            break
        rounds += 1
        slack = {t.vehicle_id: _departure_soc(t, levels[t.vehicle_id]) - t.required_pct for t in tasks}
        touched: set[int] = set()
        for slot in bad:
            excess_kw, excess_n = load[slot] - site.cap_kw, active[slot] - site.connectors
            for v in sorted((v for v in levels if levels[v][slot] > 0), key=lambda x: (-slack[x], x)):
                if excess_kw <= EPS and excess_n <= 0:
                    break
                j, lv = index[v], int(levels[v][slot])
                new = 0 if excess_n > 0 else lv - 1
                caps[j, slot] = min(caps[j, slot], new)
                excess_kw -= tasks[j].levels_kw[lv] - tasks[j].levels_kw[new]
                excess_n -= int(new == 0)
                touched.add(j)
                repairs += 1
        rows = sorted(touched)
        sub = [tasks[j] for j in rows]
        resolved = solve_batch(sub, price[rows], model, caps[rows])
        for k, j in enumerate(rows):
            levels[tasks[j].vehicle_id] = resolved[k]
    plan = _finish("LAGRANGIAN_REPAIRED", tasks, levels, tariff, model)
    plan.iterations, plan.repairs = it, repairs
    plan.stats["repair_rounds"] = rounds
    return plan


def greedy(tasks: list[Task], tariff: Floats, site: Site, model: DegradationModel) -> SitePlan:
    """Fallback: slot by slot, serve vehicles in order of least laxity (slots left minus slots needed at
    full power), each at the highest level that fits the remaining kW, up to the connector count.
    Cap-safe by construction; price-blind. O(T · N log N)."""
    t_len = len(tariff)
    soc = {t.vehicle_id: t.soc0_pct for t in tasks}
    levels = {t.vehicle_id: np.zeros(t_len, dtype=np.int64) for t in tasks}
    for slot in range(t_len):
        here = [t for t in tasks if t.plug_slot <= slot < t.depart_slot and soc[t.vehicle_id] < t.cap_pct - EPS]

        def laxity(t: Task, slot: int = slot) -> float:
            need = max(0.0, t.required_pct - soc[t.vehicle_id]) / max(t.soc_gain_pct(t.levels_kw[-1]), EPS)
            return (t.depart_slot - slot) - need

        room, n = site.cap_kw, 0
        for t in sorted(here, key=lambda x: (laxity(x), x.vehicle_id)):
            if n >= site.connectors:
                break
            if soc[t.vehicle_id] >= max(t.required_pct, 0) and laxity(t) > 0 and slot < t.depart_slot - 1:
                continue  # already meets its departure: leave the room to others
            idx = max((i for i, p in enumerate(t.levels_kw) if p <= room + EPS), default=0)
            if idx == 0:
                continue
            levels[t.vehicle_id][slot] = idx
            room -= t.levels_kw[idx]
            n += 1
            soc[t.vehicle_id] = min(t.cap_pct, soc[t.vehicle_id] + t.soc_gain_pct(t.levels_kw[idx]))
    return _finish("GREEDY_FALLBACK", tasks, levels, tariff, model)


def baseline(tasks: list[Task], tariff: Floats, model: DegradationModel) -> SitePlan:
    """Uncontrolled charge-on-arrival (ignores the site cap and connector count: that is the point)."""
    t_len = len(tariff)
    return _finish(
        "CHARGE_ON_ARRIVAL", tasks, {t.vehicle_id: charge_on_arrival(t, t_len) for t in tasks}, tariff, model
    )
