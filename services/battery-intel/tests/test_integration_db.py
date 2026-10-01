"""Postgres store against the local stack (make up): idempotency and RLS tenant isolation.

Connects as battery_app (non-superuser), so RLS is really enforced. Run: uv run pytest -m integration
"""

from __future__ import annotations

import asyncio
import os
import uuid
from collections.abc import Coroutine, Iterator
from typing import Any

import psycopg
import pytest

from app.application.soh import Estimate, Step
from app.domain.soh import KalmanState, Observation, kalman_update
from app.infrastructure.db import PgSohReader, PgSohStore

pytestmark = pytest.mark.integration
DSN = os.environ.get("BATTERY_DSN", "postgresql://battery_app:battery-app-dev-only@localhost:5432/kilowatt")
VIN = "0KCDV45N0MB000239"


def _run[T](coro: Coroutine[Any, Any, T]) -> T:  # psycopg async cannot use the Windows Proactor loop
    return asyncio.run(coro, loop_factory=asyncio.SelectorEventLoop)


def _step(tenant: str, pack: str, session: str, as_of_ms: int) -> Step:
    def step(prev: KalmanState | None) -> tuple[KalmanState, Estimate]:
        st = kalman_update(prev, Observation(95.0, 1.0, 60.0), as_of_ms)
        lo, hi = st.ci95()
        return st, Estimate(tenant, VIN, pack, session, as_of_ms, st.soh_pct, lo, hi, 95.0, "test")

    return step


@pytest.fixture
def store() -> Iterator[PgSohStore]:
    s = PgSohStore(DSN)  # no skip: `-m integration` against a missing stack must fail, not pass empty
    yield s
    s.close()


def test_same_session_twice_is_one_estimate_and_one_state_step(store: PgSohStore) -> None:
    tenant, pack, session = str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
    assert store.apply(tenant, pack, session, _step(tenant, pack, session, 1_000)) is not None
    assert store.apply(tenant, pack, session, _step(tenant, pack, session, 1_000)) is None
    second = str(uuid.uuid4())
    assert store.apply(tenant, pack, second, _step(tenant, pack, second, 86_400_000)) is not None
    with psycopg.connect(DSN) as c, c.transaction():
        c.execute("SELECT set_config('app.tenant_id', %s, true)", (tenant,))
        n = c.execute("SELECT n FROM battery.pack_kf_state WHERE pack_id = %s", (pack,)).fetchone()
        rows = c.execute("SELECT count(*) FROM battery.soh_estimate WHERE pack_id = %s", (pack,)).fetchone()
    assert n == (2,) and rows == (2,)


def test_rls_hides_other_tenants_and_rejects_cross_tenant_writes(store: PgSohStore) -> None:
    tenant, other, pack, session = (str(uuid.uuid4()) for _ in range(4))
    store.apply(tenant, pack, session, _step(tenant, pack, session, 1_000))
    reader = PgSohReader(DSN)
    assert any(e.pack_id == pack for e in _run(reader.history(tenant, VIN)))
    assert not any(e.pack_id == pack for e in _run(reader.history(other, VIN)))
    with psycopg.connect(DSN) as c:  # no tenant set: RLS returns nothing
        assert c.execute("SELECT count(*) FROM battery.soh_estimate").fetchone() == (0,)
    # The store's transaction runs as `other`, but the estimate claims `tenant`: WITH CHECK refuses.
    with pytest.raises(psycopg.errors.InsufficientPrivilege):
        store.apply(other, str(uuid.uuid4()), session, _step(tenant, pack, session, 2_000))
