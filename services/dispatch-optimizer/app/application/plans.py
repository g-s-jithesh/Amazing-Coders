"""Use cases: create a DRAFT depot plan, approve it (→ outbox → dispatch.commands.v1), read it.

Ports only. Money: each assignment's cost is rounded to paise once, and the plan's energy cost is the
exact sum of its assignments (tests assert this).
"""

from __future__ import annotations

import uuid
from collections.abc import Callable
from dataclasses import dataclass
from typing import Protocol

import numpy as np

from app.application.planning import HORIZON_SLOTS, DepotState, SolverConfig, plan_depot
from app.domain.connectors import assign
from app.domain.degradation import DegradationModel
from app.domain.money import to_paise
from app.domain.plan import Assignment, PlanRecord, Status, transition
from app.domain.schedule import DT_H
from app.domain.tariff import SLOT_MS, TariffWindow, slot_prices

COMMANDS_TOPIC = "dispatch.commands.v1"


class FleetPort(Protocol):
    def depot_state(self, tenant_id: str, depot_id: str, now_ms: int) -> DepotState | None: ...


class PlanStore(Protocol):
    def by_idempotency_key(self, tenant_id: str, key: str) -> PlanRecord | None: ...
    def next_version(self, tenant_id: str, depot_id: str) -> int: ...
    def insert(self, plan: PlanRecord, idempotency_key: str | None) -> None: ...
    def get(self, tenant_id: str, plan_id: str) -> PlanRecord | None: ...
    def approve(self, plan: PlanRecord, payload: bytes) -> None:
        """One transaction: set APPROVED (+approved_by/at) only if still DRAFT, mark the depot's other
        APPROVED/PUBLISHED plans SUPERSEDED, and insert the outbox row (topic, key=depot_id, payload)."""
        ...


class NotFound(Exception):
    pass


@dataclass
class Plans:
    fleet: FleetPort
    store: PlanStore
    tariff: list[TariffWindow]
    tariff_code: str
    model: DegradationModel
    cfg: SolverConfig
    encode: Callable[[PlanRecord], bytes]
    clock_ms: Callable[[], int]

    def create(self, tenant_id: str, depot_id: str, user: str, idempotency_key: str | None) -> tuple[PlanRecord, bool]:
        """Returns (plan, created). A repeated Idempotency-Key returns the original plan, created=False."""
        if idempotency_key:
            prev = self.store.by_idempotency_key(tenant_id, idempotency_key)
            if prev is not None:
                return prev, False
        now = self.clock_ms()
        start = now - now % SLOT_MS
        depot = self.fleet.depot_state(tenant_id, depot_id, now)
        if depot is None:  # unknown depot or another tenant's: the same 404 (no enumeration)
            raise NotFound(depot_id)
        prices = slot_prices(self.tariff, start, HORIZON_SLOTS)
        res = plan_depot(depot, start, prices, self.model, self.cfg)

        power: dict[str, np.ndarray] = {}
        for st in res.stays:
            p = res.plan.evaluations[st.task.vehicle_id].power_kw
            power[st.vehicle_id] = power.get(st.vehicle_id, np.zeros(HORIZON_SLOTS)) + p
        rows = []
        for r in assign(power, depot.connectors):
            seg = prices[r.slot_start : r.slot_end]
            rows.append(
                Assignment(
                    vehicle_id=r.vehicle_id,
                    connector_index=r.connector,
                    start_ms=start + r.slot_start * SLOT_MS,
                    end_ms=start + r.slot_end * SLOT_MS,
                    power_kw=round(r.power_kw, 3),
                    energy_kwh=round(r.power_kw * DT_H * len(seg), 3),
                    cost_paise=to_paise(float(seg.sum()) * r.power_kw * DT_H),
                )
            )
        energy = sum(a.cost_paise for a in rows)
        plan = PlanRecord(
            id=str(uuid.uuid4()),
            tenant_id=tenant_id,
            depot_id=depot_id,
            version=self.store.next_version(tenant_id, depot_id),
            status=Status.DRAFT,
            method=res.method,
            tariff_code=self.tariff_code,
            horizon_start_ms=start,
            horizon_slots=HORIZON_SLOTS,
            cost_paise=energy + res.degradation_cost_paise,
            energy_cost_paise=energy,
            degradation_cost_paise=res.degradation_cost_paise,
            baseline_cost_paise=res.baseline_cost_paise,
            baseline_energy_cost_paise=res.baseline_energy_cost_paise,
            baseline_degradation_cost_paise=res.baseline_degradation_cost_paise,
            missed_departures=len(res.plan.infeasible),
            created_by=user,
            created_at_ms=now,
            solver_stats={
                "iterations": float(res.plan.iterations),
                "repairs": float(res.plan.repairs),
                "stays": float(len(res.stays)),
                "peak_kw": round(float(res.plan.load_kw.max()), 3) if len(res.stays) else 0.0,
                "baseline_peak_kw": round(float(res.base.load_kw.max()), 3) if len(res.stays) else 0.0,
                "site_cap_kw": depot.site_cap_kw,
                "connectors": float(depot.connectors),
            },
            assignments=rows,
        )
        self.store.insert(plan, idempotency_key)
        return plan, True

    def get(self, tenant_id: str, plan_id: str) -> PlanRecord:
        plan = self.store.get(tenant_id, plan_id)
        if plan is None:
            raise NotFound(plan_id)
        return plan

    def approve(self, tenant_id: str, plan_id: str, user: str) -> PlanRecord:
        """Idempotent: approving an already APPROVED/PUBLISHED plan returns it unchanged."""
        plan = self.get(tenant_id, plan_id)
        if plan.status in (Status.APPROVED, Status.PUBLISHED):
            return plan
        plan.status = transition(plan.status, Status.APPROVED)
        plan.approved_by, plan.approved_at_ms = user, self.clock_ms()
        self.store.approve(plan, self.encode(plan))
        return plan
