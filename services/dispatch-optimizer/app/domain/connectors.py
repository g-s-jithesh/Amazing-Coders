"""Turn per-slot power profiles into connector assignments (the rows a dispatcher acts on).

The plan already guarantees ≤ `connectors` vehicles charge in any slot; this picks *which* connector.
Sticky: a vehicle keeps its connector while it charges in consecutive slots; otherwise it takes the
lowest free index. A connector is never given to two vehicles in the same slot. Rows are maximal runs
of (vehicle, connector, constant power). O(T · N).
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
from numpy.typing import NDArray


@dataclass(frozen=True, slots=True)
class Run:
    vehicle_id: str
    connector: int
    slot_start: int
    slot_end: int  # exclusive
    power_kw: float


class TooManyVehicles(ValueError):
    pass


def assign(power: dict[str, NDArray[np.float64]], connectors: int, eps: float = 1e-9) -> list[Run]:
    vids = sorted(power)
    t_len = len(next(iter(power.values()))) if power else 0
    holder: dict[str, int] = {}
    runs: list[Run] = []
    open_run: dict[str, Run] = {}
    for t in range(t_len):
        charging = [v for v in vids if power[v][t] > eps]
        if len(charging) > connectors:
            raise TooManyVehicles(f"slot {t}: {len(charging)} vehicles > {connectors} connectors")
        keep = {v: holder[v] for v in charging if v in holder}
        free = sorted(set(range(connectors)) - set(keep.values()))
        holder = dict(keep)
        for v in charging:
            if v not in holder:
                holder[v] = free.pop(0)
        for v in list(open_run):
            r = open_run[v]
            if v not in holder or holder[v] != r.connector or abs(power[v][t] - r.power_kw) > eps:
                runs.append(Run(v, r.connector, r.slot_start, t, r.power_kw))
                del open_run[v]
        for v in charging:
            if v not in open_run:
                open_run[v] = Run(v, holder[v], t, t + 1, float(power[v][t]))
    runs.extend(Run(v, r.connector, r.slot_start, t_len, r.power_kw) for v, r in open_run.items())
    return sorted(runs, key=lambda r: (r.slot_start, r.connector))
