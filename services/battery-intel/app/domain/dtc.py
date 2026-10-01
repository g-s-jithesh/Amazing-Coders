"""DTC decoding (SAE J2012 code structure) — deterministic, no LLM. Pure. O(1).

Structure of a code like P0A7E:
  P / C / B / U          system: powertrain, chassis, body, network
  0 / 2  (and P3 low)    SAE-defined (generic); 1 and 3 are manufacturer-specific (P3 is split)
  remaining 3 hex chars  fault within the system

Descriptions come from data/reference/dtc_catalogue.csv; they are marked ``source_status`` until
checked against SAE J2012 (see root CLAUDE.md §6).
"""

from __future__ import annotations

import re
from dataclasses import dataclass

CODE_RE = re.compile(r"^[PCBU][0-3][0-9A-F]{3}$")

SYSTEMS = {"P": "powertrain", "C": "chassis", "B": "body", "U": "network"}


@dataclass(frozen=True, slots=True)
class CatalogueEntry:
    code: str
    system: str
    severity: str
    description: str
    sim_fault: str
    runbook_id: str
    source_status: str


@dataclass(frozen=True, slots=True)
class Decoded:
    code: str
    system: str
    defined_by: str  # "SAE" | "manufacturer"
    catalogued: bool
    severity: str | None
    description: str | None
    runbook_id: str | None
    source_status: str | None


class InvalidCode(ValueError):
    pass


def defined_by(code: str) -> str:
    """0 and 2: SAE-defined; 1: manufacturer-specific; 3: split between the two by sub-range (the
    exact split is not encoded here until verified against SAE J2012)."""
    return {"0": "SAE", "1": "manufacturer", "2": "SAE", "3": "range-dependent"}[code[1]]


def decode(code: str, catalogue: dict[str, CatalogueEntry]) -> Decoded:
    code = code.strip().upper()
    if not CODE_RE.match(code):
        raise InvalidCode(code)
    e = catalogue.get(code)
    return Decoded(
        code=code,
        system=SYSTEMS[code[0]],
        defined_by=defined_by(code),
        catalogued=e is not None,
        severity=e.severity if e else None,
        description=e.description if e else None,
        runbook_id=e.runbook_id if e else None,
        source_status=e.source_status if e else None,
    )
