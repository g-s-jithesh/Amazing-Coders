"""Independent plan validator: the source of truth for plan invariants. It recomputes everything from
the tasks and the power profile alone (exact arithmetic), sharing no code path with the solvers. O(N·T)."""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np

from app.domain.coordinate import Site
from app.domain.schedule import Floats, Task

TOL = 1e-6


@dataclass(frozen=True, slots=True)
class Violation:
    kind: str  # LEVEL | OUTSIDE_WINDOW | SOC_RANGE | DEPARTURE | SITE_CAP | CONNECTORS
    vehicle_id: str | None
    slot: int | None
    detail: str


def validate(tasks: list[Task], power: dict[str, Floats], site: Site | None, t_len: int) -> list[Violation]:
    """``site=None`` skips the coupling checks (used for the uncontrolled baseline)."""
    out: list[Violation] = []
    load = np.zeros(t_len)
    active = np.zeros(t_len, dtype=np.int64)
    for t in tasks:
        p = power[t.vehicle_id]
        soc = t.soc0_pct
        for k in range(t_len):
            pk = float(p[k])
            gain = t.eta * pk * 0.25 / t.usable_kwh * 100.0
            tapered = abs(soc + gain - t.cap_pct) <= TOL and pk <= t.levels_kw[-1] + TOL  # charger tapers at full
            if not tapered and not any(abs(pk - lv) <= TOL for lv in t.levels_kw):
                out.append(Violation("LEVEL", t.vehicle_id, k, f"{pk} kW is not an allowed level"))
            if pk > TOL and not t.plug_slot <= k < t.depart_slot:
                out.append(Violation("OUTSIDE_WINDOW", t.vehicle_id, k, "power while not plugged in"))
            soc += gain
            if soc > t.cap_pct + TOL or soc < -TOL:
                out.append(Violation("SOC_RANGE", t.vehicle_id, k, f"SoC {soc:.3f} outside [0, {t.cap_pct}]"))
            if k + 1 == t.depart_slot and soc < t.required_pct - TOL:
                out.append(Violation("DEPARTURE", t.vehicle_id, k + 1, f"SoC {soc:.2f} < {t.required_pct}"))
        load += p
        active += p > TOL
    if site is not None:
        for k in np.nonzero(load > site.cap_kw + TOL)[0].tolist():
            out.append(Violation("SITE_CAP", None, k, f"{load[k]:.1f} kW > {site.cap_kw} kW"))
        for k in np.nonzero(active > site.connectors)[0].tolist():
            out.append(Violation("CONNECTORS", None, k, f"{active[k]} vehicles > {site.connectors}"))
    return out
