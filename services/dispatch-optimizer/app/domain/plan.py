"""Dispatch plan lifecycle: DRAFT → APPROVED → PUBLISHED; a newer approval marks older live plans
SUPERSEDED. Only a human approval moves a plan out of DRAFT (the copilot can only create drafts)."""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum


class Status(StrEnum):
    DRAFT = "DRAFT"
    APPROVED = "APPROVED"
    PUBLISHED = "PUBLISHED"
    SUPERSEDED = "SUPERSEDED"


class InvalidTransition(Exception):
    def __init__(self, current: Status, target: Status) -> None:
        super().__init__(f"cannot move a {current} plan to {target}")
        self.current, self.target = current, target


_ALLOWED = {
    (Status.DRAFT, Status.APPROVED),
    (Status.APPROVED, Status.PUBLISHED),
    (Status.APPROVED, Status.SUPERSEDED),
    (Status.PUBLISHED, Status.SUPERSEDED),
    (Status.DRAFT, Status.SUPERSEDED),
}


def transition(current: Status, target: Status) -> Status:
    if (current, target) not in _ALLOWED:
        raise InvalidTransition(current, target)
    return target


@dataclass(frozen=True, slots=True)
class Assignment:
    vehicle_id: str
    connector_index: int
    start_ms: int
    end_ms: int
    power_kw: float
    energy_kwh: float
    cost_paise: int


@dataclass(slots=True)
class PlanRecord:
    id: str
    tenant_id: str
    depot_id: str
    version: int
    status: Status
    method: str
    tariff_code: str
    horizon_start_ms: int
    horizon_slots: int
    cost_paise: int
    energy_cost_paise: int
    degradation_cost_paise: int
    baseline_cost_paise: int
    baseline_energy_cost_paise: int
    baseline_degradation_cost_paise: int
    missed_departures: int
    created_by: str
    created_at_ms: int
    solver_stats: dict[str, float] = field(default_factory=dict)
    approved_by: str | None = None
    approved_at_ms: int | None = None
    assignments: list[Assignment] = field(default_factory=list)
