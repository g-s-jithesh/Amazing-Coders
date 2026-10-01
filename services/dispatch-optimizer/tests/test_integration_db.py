"""Plan store and outbox against the local stack (make up): RLS isolation, idempotency key, approve →
outbox in one transaction, and the relay role's limited scope. Run: uv run pytest -m integration
"""

from __future__ import annotations

import os
import time
import uuid

import psycopg
import pytest

from app.domain.plan import Assignment, InvalidTransition, PlanRecord, Status
from app.infrastructure.db import PgOutbox, PgPlanStore
from app.infrastructure.kafka import encode

pytestmark = pytest.mark.integration
APP = os.environ.get("DISPATCH_DSN", "postgresql://dispatch_app:dispatch-app-dev-only@localhost:5432/kilowatt")
RELAY = os.environ.get("RELAY_DSN", "postgresql://dispatch_relay:dispatch-relay-dev-only@localhost:5432/kilowatt")
T0 = 1788201000000


def plan(tenant: str, depot: str, version: int = 1) -> PlanRecord:
    vid = str(uuid.uuid4())
    return PlanRecord(
        id=str(uuid.uuid4()),
        tenant_id=tenant,
        depot_id=depot,
        version=version,
        status=Status.DRAFT,
        method="LAGRANGIAN",
        tariff_code="DEPOT_TOD_SYNTH",
        horizon_start_ms=T0,
        horizon_slots=144,
        cost_paise=150,
        energy_cost_paise=100,
        degradation_cost_paise=50,
        baseline_cost_paise=200,
        baseline_energy_cost_paise=120,
        baseline_degradation_cost_paise=80,
        missed_departures=0,
        created_by="tester",
        created_at_ms=T0,
        solver_stats={"iterations": 3.0},
        assignments=[
            Assignment(vid, 0, T0, T0 + 900_000, 7.2, 1.8, 60),
            Assignment(vid, 0, T0 + 900_000, T0 + 1_800_000, 3.6, 0.9, 40),
        ],
    )


def test_insert_get_idempotency_and_tenant_isolation() -> None:
    store = PgPlanStore(APP)
    tenant, other, depot = (str(uuid.uuid4()) for _ in range(3))
    p = plan(tenant, depot)
    store.insert(p, idempotency_key=f"key-{p.id}")
    got = store.get(tenant, p.id)
    assert got is not None and got.status == Status.DRAFT and len(got.assignments) == 2
    assert sum(a.cost_paise for a in got.assignments) == got.energy_cost_paise
    assert store.by_idempotency_key(tenant, f"key-{p.id}") is not None
    assert store.get(other, p.id) is None  # RLS: invisible to another tenant
    assert store.by_idempotency_key(other, f"key-{p.id}") is None
    assert store.next_version(tenant, depot) == 2


def test_approve_supersedes_writes_outbox_and_relay_publishes() -> None:
    store = PgPlanStore(APP)
    tenant, depot = str(uuid.uuid4()), str(uuid.uuid4())
    old, new = plan(tenant, depot, 1), plan(tenant, depot, 2)
    store.insert(old, None)
    store.insert(new, None)
    new.approved_by, new.approved_at_ms = "dispatcher-1", T0
    payload = encode(new)  # a real contract message: the stack's relay may publish this row
    store.approve(new, payload)
    with pytest.raises(InvalidTransition):  # already approved: the conditional UPDATE refuses
        store.approve(new, payload)
    assert store.get(tenant, old.id).status == Status.SUPERSEDED  # type: ignore[union-attr]

    with psycopg.connect(RELAY) as c:  # exactly one command, written in the approval transaction
        rows = c.execute("SELECT key, payload FROM dispatch.outbox WHERE plan_id = %s", (new.id,)).fetchall()
    assert len(rows) == 1 and rows[0][0] == depot and bytes(rows[0][1]) == payload
    # Publish it here unless the stack's own relay container already did (either way it ends PUBLISHED).
    relay = PgOutbox(RELAY)
    mine = [r for r in relay.claim(1000) if r["plan_id"] == new.id]
    if mine:
        relay.mark_published([r["id"] for r in mine], [new.id])
    else:
        relay.rollback()
    relay.close()
    deadline = time.monotonic() + 10
    while store.get(tenant, new.id).status != Status.PUBLISHED and time.monotonic() < deadline:  # type: ignore[union-attr]
        time.sleep(0.2)
    assert store.get(tenant, new.id).status == Status.PUBLISHED  # type: ignore[union-attr]


def test_relay_role_cannot_read_drafts_or_assignments() -> None:
    store = PgPlanStore(APP)
    tenant, depot = str(uuid.uuid4()), str(uuid.uuid4())
    p = plan(tenant, depot)
    store.insert(p, None)
    with psycopg.connect(RELAY) as c:
        assert c.execute("SELECT count(*) FROM dispatch.dispatch_plan WHERE id = %s", (p.id,)).fetchone() == (0,)
        with pytest.raises(psycopg.errors.InsufficientPrivilege):
            c.execute("SELECT count(*) FROM dispatch.dispatch_assignment")
