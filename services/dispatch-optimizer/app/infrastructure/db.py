"""Postgres plan store (psycopg 3, sync; FastAPI runs these handlers in its threadpool). Every statement runs
under RLS: the transaction first sets app.tenant_id, and the service connects as the non-superuser
dispatch_app role.

ponytail: one connection per call; add psycopg_pool when the API p95 is measured under load.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

import psycopg
from psycopg.rows import dict_row
from psycopg.types.json import Jsonb

from app.application.plans import COMMANDS_TOPIC
from app.domain.plan import Assignment, InvalidTransition, PlanRecord, Status

SET_TENANT = "SELECT set_config('app.tenant_id', %s, true)"
PLAN_COLS = """id::text, tenant_id::text, depot_id::text, version, status, method, tariff_code, horizon_start,
  horizon_slots, cost_paise, energy_cost_paise, degradation_cost_paise, baseline_cost_paise,
  baseline_energy_cost_paise, baseline_degradation_cost_paise, missed_departures, solver_stats, created_by,
  created_at, approved_by, approved_at"""


def _ms(dt: datetime | None) -> int | None:
    return None if dt is None else int(dt.timestamp() * 1000)


def _dt(ms: int) -> datetime:
    return datetime.fromtimestamp(ms / 1000, tz=UTC)


def _plan(r: dict[str, Any], rows: list[dict[str, Any]]) -> PlanRecord:
    return PlanRecord(
        id=r["id"],
        tenant_id=r["tenant_id"],
        depot_id=r["depot_id"],
        version=r["version"],
        status=Status(r["status"]),
        method=r["method"],
        tariff_code=r["tariff_code"],
        horizon_start_ms=_ms(r["horizon_start"]) or 0,
        horizon_slots=r["horizon_slots"],
        cost_paise=r["cost_paise"],
        energy_cost_paise=r["energy_cost_paise"],
        degradation_cost_paise=r["degradation_cost_paise"],
        baseline_cost_paise=r["baseline_cost_paise"],
        baseline_energy_cost_paise=r["baseline_energy_cost_paise"],
        baseline_degradation_cost_paise=r["baseline_degradation_cost_paise"],
        missed_departures=r["missed_departures"],
        created_by=r["created_by"],
        created_at_ms=_ms(r["created_at"]) or 0,
        solver_stats=r["solver_stats"],
        approved_by=r["approved_by"],
        approved_at_ms=_ms(r["approved_at"]),
        assignments=[
            Assignment(
                vehicle_id=a["vehicle_id"],
                connector_index=a["connector_index"],
                start_ms=_ms(a["slot_start"]) or 0,
                end_ms=_ms(a["slot_end"]) or 0,
                power_kw=float(a["power_kw"]),
                energy_kwh=float(a["energy_kwh"]),
                cost_paise=a["cost_paise"],
            )
            for a in rows
        ],
    )


class PgPlanStore:
    def __init__(self, dsn: str) -> None:
        self.dsn = dsn

    def _tx(self, tenant_id: str) -> psycopg.Connection[dict[str, Any]]:
        conn = psycopg.connect(self.dsn, row_factory=dict_row)
        conn.execute(SET_TENANT, (tenant_id,))
        return conn

    def by_idempotency_key(self, tenant_id: str, key: str) -> PlanRecord | None:
        with self._tx(tenant_id) as c:
            r = c.execute(
                f"SELECT {PLAN_COLS} FROM dispatch.dispatch_plan WHERE idempotency_key = %s",  # noqa: S608 - constant
                (key,),
            ).fetchone()
            return None if r is None else self._load(c, r)

    def next_version(self, tenant_id: str, depot_id: str) -> int:
        with self._tx(tenant_id) as c:
            r = c.execute(
                "SELECT coalesce(max(version), 0) + 1 AS v FROM dispatch.dispatch_plan WHERE depot_id = %s",
                (depot_id,),
            ).fetchone()
            return int(r["v"]) if r else 1

    def insert(self, plan: PlanRecord, idempotency_key: str | None) -> None:
        with self._tx(plan.tenant_id) as c:
            c.execute(
                """INSERT INTO dispatch.dispatch_plan (id, tenant_id, depot_id, version, status, method, tariff_code,
                     horizon_start, horizon_slots, cost_paise, energy_cost_paise, degradation_cost_paise,
                     baseline_cost_paise, baseline_energy_cost_paise, baseline_degradation_cost_paise,
                     missed_departures, solver_stats, created_by, created_at, idempotency_key)
                   VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
                (
                    plan.id,
                    plan.tenant_id,
                    plan.depot_id,
                    plan.version,
                    plan.status.value,
                    plan.method,
                    plan.tariff_code,
                    _dt(plan.horizon_start_ms),
                    plan.horizon_slots,
                    plan.cost_paise,
                    plan.energy_cost_paise,
                    plan.degradation_cost_paise,
                    plan.baseline_cost_paise,
                    plan.baseline_energy_cost_paise,
                    plan.baseline_degradation_cost_paise,
                    plan.missed_departures,
                    Jsonb(plan.solver_stats),
                    plan.created_by,
                    _dt(plan.created_at_ms),
                    idempotency_key,
                ),
            )
            with c.cursor() as cur:
                cur.executemany(
                    """INSERT INTO dispatch.dispatch_assignment (plan_id, tenant_id, vehicle_id, connector_index,
                         slot_start, slot_end, power_kw, energy_kwh, cost_paise) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)""",
                    [
                        (
                            plan.id,
                            plan.tenant_id,
                            a.vehicle_id,
                            a.connector_index,
                            _dt(a.start_ms),
                            _dt(a.end_ms),
                            a.power_kw,
                            a.energy_kwh,
                            a.cost_paise,
                        )
                        for a in plan.assignments
                    ],
                )

    def get(self, tenant_id: str, plan_id: str) -> PlanRecord | None:
        with self._tx(tenant_id) as c:
            r = c.execute(
                f"SELECT {PLAN_COLS} FROM dispatch.dispatch_plan WHERE id = %s",  # noqa: S608 - constant columns
                (plan_id,),
            ).fetchone()
            return None if r is None else self._load(c, r)

    def _load(self, c: psycopg.Connection[dict[str, Any]], r: dict[str, Any]) -> PlanRecord:
        rows = c.execute(
            """SELECT vehicle_id::text, connector_index, slot_start, slot_end, power_kw, energy_kwh, cost_paise
               FROM dispatch.dispatch_assignment WHERE plan_id = %s ORDER BY slot_start, connector_index""",
            (r["id"],),
        ).fetchall()
        return _plan(r, rows)

    def approve(self, plan: PlanRecord, payload: bytes) -> None:
        with self._tx(plan.tenant_id) as c:
            done = c.execute(
                """UPDATE dispatch.dispatch_plan SET status = 'APPROVED', approved_by = %s, approved_at = %s
                   WHERE id = %s AND status = 'DRAFT' RETURNING id""",
                (plan.approved_by, _dt(plan.approved_at_ms or 0), plan.id),
            ).fetchone()
            if done is None:  # changed concurrently since we read it
                raise InvalidTransition(Status.SUPERSEDED, Status.APPROVED)
            c.execute(
                """UPDATE dispatch.dispatch_plan SET status = 'SUPERSEDED'
                   WHERE depot_id = %s AND id <> %s AND status IN ('APPROVED', 'PUBLISHED', 'DRAFT')
                     AND version < %s""",
                (plan.depot_id, plan.id, plan.version),
            )
            c.execute(
                "INSERT INTO dispatch.outbox (tenant_id, topic, key, payload, plan_id) VALUES (%s, %s, %s, %s, %s)",
                (plan.tenant_id, COMMANDS_TOPIC, plan.depot_id, payload, plan.id),
            )

    def ping(self) -> None:
        with psycopg.connect(self.dsn) as c:
            c.execute("SELECT 1")


class PgOutbox:
    """Relay side: connects as dispatch_relay (sees every tenant's outbox; touches nothing else)."""

    def __init__(self, dsn: str) -> None:
        self.conn = psycopg.connect(dsn, row_factory=dict_row)

    def claim(self, limit: int = 100) -> list[dict[str, Any]]:
        """Open a transaction and lock unpublished rows (SKIP LOCKED: relays may run in parallel)."""
        return self.conn.execute(
            """SELECT id, topic, key, payload, plan_id::text FROM dispatch.outbox WHERE published_at IS NULL
               ORDER BY id LIMIT %s FOR UPDATE SKIP LOCKED""",
            (limit,),
        ).fetchall()

    def mark_published(self, ids: list[int], plan_ids: list[str]) -> None:
        self.conn.execute("UPDATE dispatch.outbox SET published_at = now() WHERE id = ANY(%s)", (ids,))
        self.conn.execute(
            "UPDATE dispatch.dispatch_plan SET status = 'PUBLISHED' WHERE id = ANY(%s::uuid[]) AND status = 'APPROVED'",
            (plan_ids,),
        )
        self.conn.commit()

    def rollback(self) -> None:
        self.conn.rollback()

    def close(self) -> None:
        self.conn.close()
