"""Time-of-use tariffs → a price per 15-min slot.

Windows are in IST (UTC+05:30, no DST) minute-of-day; ``end_min < start_min`` means the window crosses
midnight and belongs to the day it starts on. Slots are aligned to IST quarter-hours, which are also UTC
quarter-hours because 5 h 30 min is a multiple of 15 min. O(T·W).
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np
from numpy.typing import NDArray

SLOT_MIN = 15
SLOT_MS = SLOT_MIN * 60_000
IST_OFFSET_MS = 330 * 60_000
DAY_MIN = 1440


@dataclass(frozen=True, slots=True)
class TariffWindow:
    dow: int | None  # 0 = Monday (IST) … 6; None = every day
    start_min: int
    end_min: int  # exclusive
    price_paise_per_kwh: int


class TariffGap(ValueError):
    """A slot is covered by no window: a tariff must cover the whole week."""


def _covers(w: TariffWindow, dow: int, minute: int) -> bool:
    if w.start_min <= w.end_min:
        return (w.dow is None or w.dow == dow) and w.start_min <= minute < w.end_min
    # crosses midnight: [start, 1440) on its own day, [0, end) on the next
    if minute >= w.start_min:
        return w.dow is None or w.dow == dow
    return minute < w.end_min and (w.dow is None or w.dow == (dow - 1) % 7)


def slot_prices(windows: list[TariffWindow], start_utc_ms: int, slots: int) -> NDArray[np.float64]:
    """Price (paise/kWh) of each slot starting at start_utc_ms (must be slot-aligned). The first window
    listed wins when windows overlap, so specific days can be listed before an every-day default."""
    if start_utc_ms % SLOT_MS:
        raise ValueError("start must be aligned to a 15-min slot")
    out = np.empty(slots, dtype=np.float64)
    for i in range(slots):
        ist_min = (start_utc_ms + IST_OFFSET_MS) // 60_000 + i * SLOT_MIN
        day, minute = divmod(ist_min, DAY_MIN)
        dow = (day + 3) % 7  # 1970-01-01 was a Thursday (3)
        for w in windows:
            if _covers(w, dow, minute):
                out[i] = w.price_paise_per_kwh
                break
        else:
            raise TariffGap(f"no tariff window covers IST day {dow} minute {minute}")
    return out
