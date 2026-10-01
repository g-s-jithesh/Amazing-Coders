"""Money is integer paise at every boundary (root CLAUDE.md §4). Internals use float64; convert once, here."""

from __future__ import annotations

from decimal import ROUND_HALF_EVEN, Decimal


def to_paise(x: float) -> int:
    """Half-even rounding to whole paise, so systematic .5 cases do not drift a total upwards."""
    return int(Decimal(repr(float(x))).quantize(Decimal(1), rounding=ROUND_HALF_EVEN))
