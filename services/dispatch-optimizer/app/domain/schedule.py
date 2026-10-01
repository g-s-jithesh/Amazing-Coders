"""Single-vehicle charging schedule: dynamic programming over (15-min slot, 1-pp SoC bucket).

State s ∈ {0..100} pp, action = a power level. Transition s' = min(cap, s + ⌊η·P·Δt / usable_kwh · 100⌋): flooring
makes the DP's SoC a lower bound of the true SoC, so a plan that meets the departure in the DP meets it
in reality. Cost per slot = price[t]·P_billed·Δt (grid-side kWh; the charger tapers at the cap, so only
the energy that fits is billed) + degradation(s, P). Terminal cost = a
lexicographic penalty per pp below the required SoC, so an infeasible task still gets the best-effort
plan (least shortfall, then cheapest) and is flagged, never silently violated.

O(T·S·P) per vehicle (96 × 101 × ≤ 6), vectorised over vehicles and S with numpy.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

import numpy as np
from numpy.typing import NDArray

from app.domain.degradation import DegradationModel

DT_H = 0.25
SOC_BUCKETS = 101
SHORTFALL_PENALTY = 1e12  # paise per pp short: dominates any real cost
Floats = NDArray[np.float64]
Ints = NDArray[np.int64]


@dataclass(frozen=True, slots=True)
class Task:
    vehicle_id: str
    nominal_kwh: float
    soh_pct: float
    soc0_pct: float  # at plug-in
    required_pct: float  # at departure
    plug_slot: int  # first slot the vehicle is plugged in
    depart_slot: int  # first slot it is gone (exclusive); SoC is checked at the start of this slot
    levels_kw: tuple[float, ...]  # ascending, starts at 0: min(charger, vehicle) power, discretised
    eta: float = 0.92  # charger-to-pack efficiency
    cap_pct: float = 100.0
    temp_c: float = 25.0

    @property
    def usable_kwh(self) -> float:
        return self.nominal_kwh * self.soh_pct / 100.0

    def soc_gain_pct(self, power_kw: float) -> float:
        return self.eta * power_kw * DT_H / self.usable_kwh * 100.0


@dataclass(frozen=True, slots=True)
class Evaluation:
    """A schedule replayed with exact arithmetic against the real tariff (no Lagrangian prices)."""

    power_kw: Floats  # (T,)
    soc_pct: Floats  # (T+1,) SoC at the start of each slot
    energy_kwh: float
    energy_cost: float  # paise, float until the plan output converts once
    degradation_cost: float
    shortfall_pct: float

    @property
    def feasible(self) -> bool:
        return self.shortfall_pct <= 1e-9


def solve(task: Task, price: Floats, model: DegradationModel, max_level: Ints | None = None) -> Ints:
    """Optimal power-level index per slot for one vehicle (see ``solve_batch``)."""
    ml = None if max_level is None else max_level[None, :]
    return np.asarray(solve_batch([task], price[None, :], model, ml)[0], dtype=np.int64)


def solve_batch(tasks: list[Task], price: Floats, model: DegradationModel, max_level: Ints | None = None) -> Ints:
    """Independent DPs for N vehicles at once, vectorised over (vehicle, SoC): returns (N, T) level indices.

    ``price`` is (N, T) paise/kWh and may include Lagrangian site prices; ``max_level`` (N, T) optionally
    caps the level index per slot (cap repair). Outside a vehicle's plug window the only action is 0 at
    no cost. Ties keep the lower power level."""
    n, t_len = price.shape
    p_max = max(len(tk.levels_kw) for tk in tasks)
    levels = np.zeros((n, p_max))
    n_levels = np.array([len(tk.levels_kw) for tk in tasks])
    for j, tk in enumerate(tasks):
        levels[j, : len(tk.levels_kw)] = tk.levels_kw
    eta = np.array([tk.eta for tk in tasks])[:, None]
    usable = np.array([tk.usable_kwh for tk in tasks])[:, None]
    nominal = np.array([tk.nominal_kwh for tk in tasks])[:, None, None]
    temp = np.array([tk.temp_c for tk in tasks])[:, None, None]
    inc = np.floor(eta * levels * DT_H / usable * 100.0 + 1e-9).astype(np.int64)  # (N, P)
    s = np.arange(SOC_BUCKETS, dtype=np.int64)
    cap_b = np.array([math.floor(tk.cap_pct) for tk in tasks])[:, None]
    req_b = np.array([math.ceil(tk.required_pct - 1e-9) for tk in tasks])[:, None]
    lo = np.array([tk.plug_slot for tk in tasks])
    hi = np.array([tk.depart_slot for tk in tasks])
    # The charger tapers at the cap: only the power that still fits is delivered, billed and stresses the
    # pack. It depends on (vehicle, level, SoC bucket) but not on the slot, so it is precomputed once.
    fits_kw = np.maximum(0, cap_b - s[None, :]) * usable / (eta * DT_H * 100.0)  # (N, S)
    billed = np.minimum(levels[:, :, None], fits_kw[:, None, :])  # (N, P, S)
    deg = model.slot_cost(s.astype(np.float64), billed, usable[:, :, None], nominal, temp, DT_H)
    deg = np.broadcast_to(deg, (n, p_max, SOC_BUCKETS))
    value: Floats = SHORTFALL_PENALTY * np.maximum(0, req_b - s[None, :]).astype(np.float64)
    choice = np.zeros((t_len, n, SOC_BUCKETS), dtype=np.int8)
    top = np.broadcast_to((n_levels - 1)[:, None], (n, t_len)) if max_level is None else max_level
    top = np.minimum(top, (n_levels - 1)[:, None])
    for t in range(t_len - 1, -1, -1):
        idx = np.nonzero((lo <= t) & (t < hi))[0]  # only plugged-in vehicles have a decision at t
        if len(idx) == 0:
            continue
        v = value[idx]
        best = v + deg[idx, 0, :]  # action 0: stay; costs only its degradation
        arg = np.zeros((len(idx), SOC_BUCKETS), dtype=np.int8)
        top_t = top[idx, t]
        for i in range(1, p_max):
            allowed = i <= top_t
            if not allowed.any():
                continue
            nxt = np.minimum(s[None, :] + inc[idx, i : i + 1], cap_b[idx])  # taper: the SoC stops at the cap
            fut = v[np.arange(len(idx))[:, None], nxt]
            c = price[idx, t : t + 1] * billed[idx, i, :] * DT_H + deg[idx, i, :] + fut
            better = allowed[:, None] & (c < best)
            best[better] = c[better]
            arg[better] = i
        value[idx] = best
        choice[t, idx] = arg

    out = np.zeros((n, t_len), dtype=np.int64)
    b = np.clip(np.floor([tk.soc0_pct for tk in tasks]).astype(np.int64), 0, SOC_BUCKETS - 1)
    rows_idx = np.arange(n)
    for t in range(t_len):
        out[:, t] = choice[t, rows_idx, b]
        b = np.minimum(b + inc[rows_idx, out[:, t]], SOC_BUCKETS - 1)
    return out


def evaluate(task: Task, level_idx: Ints, tariff: Floats, model: DegradationModel) -> Evaluation:
    levels = np.asarray(task.levels_kw, dtype=np.float64)
    power = levels[level_idx].copy()
    soc = np.empty(len(tariff) + 1)
    soc[0] = task.soc0_pct
    for t in range(len(power)):
        # The charger tapers when the pack reaches the cap: only the energy that fits is delivered (and billed).
        fits = max(0.0, (task.cap_pct - soc[t]) * task.usable_kwh / (task.eta * DT_H * 100.0))
        power[t] = min(power[t], fits)
        soc[t + 1] = soc[t] + task.soc_gain_pct(float(power[t]))
    window = slice(max(0, task.plug_slot), min(len(power), task.depart_slot))
    deg = float(
        model.slot_cost(soc[:-1][window], power[window], task.usable_kwh, task.nominal_kwh, task.temp_c, DT_H).sum()
    )
    at_departure = soc[min(task.depart_slot, len(tariff))]
    energy = power * DT_H
    return Evaluation(
        power_kw=power,
        soc_pct=soc,
        energy_kwh=float(energy.sum()),
        energy_cost=float((tariff * energy).sum()),
        degradation_cost=deg,
        shortfall_pct=max(0.0, task.required_pct - float(at_departure)),
    )


def charge_on_arrival(task: Task, t_len: int) -> Ints:
    """Naive baseline: maximum power from plug-in until full or departure."""
    out = np.zeros(t_len, dtype=np.int64)
    soc, top = task.soc0_pct, len(task.levels_kw) - 1
    for t in range(max(0, task.plug_slot), min(t_len, task.depart_slot)):
        if soc >= task.cap_pct - 1e-9:
            break
        out[t] = top
        soc = min(task.cap_pct, soc + task.soc_gain_pct(task.levels_kw[top]))
    return out
