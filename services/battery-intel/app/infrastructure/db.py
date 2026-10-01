"""Postgres adapters (psycopg 3). Every statement runs under RLS: the transaction first sets
app.tenant_id, and the service connects as the non-superuser battery_app role."""

from __future__ import annotations

from datetime import UTC, datetime

import psycopg
from psycopg.rows import dict_row

from app.application.soh import Estimate, Step
from app.domain.soh import KalmanState


def _ms(dt: datetime) -> int:
    return int(dt.timestamp() * 1000)


def _dt(ms: int) -> datetime:
    return datetime.fromtimestamp(ms / 1000, tz=UTC)


SET_TENANT = "SELECT set_config('app.tenant_id', %s, true)"


class PgSohStore:
    """Write side for the worker (sync)."""

    def __init__(self, dsn: str) -> None:
        self.conn = psycopg.connect(dsn, autocommit=False)

    def apply(self, tenant_id: str, pack_id: str, session_id: str, step: Step) -> Estimate | None:
        with self.conn.transaction(), self.conn.cursor(row_factory=dict_row) as cur:
            cur.execute(SET_TENANT, (tenant_id,))
            cur.execute(
                """SELECT soh_pct, rate_pct_per_day, p00, p01, p11, as_of, n
                   FROM battery.pack_kf_state WHERE pack_id = %s FOR UPDATE""",
                (pack_id,),
            )
            row = cur.fetchone()
            prev = (
                KalmanState(
                    row["soh_pct"],
                    row["rate_pct_per_day"],
                    row["p00"],
                    row["p01"],
                    row["p11"],
                    _ms(row["as_of"]),
                    row["n"],
                )
                if row
                else None
            )
            state, est = step(prev)
            cur.execute(
                """INSERT INTO battery.soh_estimate
                   (tenant_id, vin, pack_id, session_id, as_of, soh_pct, ci_low, ci_high, obs_soh_pct, method)
                   VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                   ON CONFLICT (pack_id, session_id) DO NOTHING RETURNING id""",
                (
                    est.tenant_id,
                    est.vin,
                    est.pack_id,
                    est.session_id,
                    _dt(est.as_of_ms),
                    est.soh_pct,
                    est.ci_low,
                    est.ci_high,
                    est.obs_soh_pct,
                    est.method,
                ),
            )
            if cur.fetchone() is None:
                return None  # already applied: leave the Kalman state untouched
            cur.execute(
                """INSERT INTO battery.pack_kf_state
                     (pack_id, tenant_id, soh_pct, rate_pct_per_day, p00, p01, p11, as_of, n)
                   VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                   ON CONFLICT (pack_id) DO UPDATE SET soh_pct = EXCLUDED.soh_pct,
                     rate_pct_per_day = EXCLUDED.rate_pct_per_day, p00 = EXCLUDED.p00, p01 = EXCLUDED.p01,
                     p11 = EXCLUDED.p11, as_of = EXCLUDED.as_of, n = EXCLUDED.n""",
                (
                    pack_id,
                    tenant_id,
                    state.soh_pct,
                    state.rate_pct_per_day,
                    state.p00,
                    state.p01,
                    state.p11,
                    _dt(state.as_of_ms),
                    state.n,
                ),
            )
            return est

    def close(self) -> None:
        self.conn.close()


class PgSohReader:
    """Read side for the API (async).

    ponytail: one connection per request (~ms locally); add psycopg_pool when API p95 is measured under load."""

    def __init__(self, dsn: str) -> None:
        self.dsn = dsn

    async def history(self, tenant_id: str, vin: str, limit: int = 500) -> list[Estimate]:
        async with await psycopg.AsyncConnection.connect(self.dsn) as conn:
            async with conn.transaction(), conn.cursor(row_factory=dict_row) as cur:
                await cur.execute(SET_TENANT, (tenant_id,))
                await cur.execute(
                    """SELECT tenant_id::text, vin, pack_id::text, session_id::text, as_of, soh_pct, ci_low, ci_high,
                              obs_soh_pct, method
                       FROM battery.soh_estimate WHERE vin = %s ORDER BY as_of DESC LIMIT %s""",
                    (vin, limit),
                )
                rows = await cur.fetchall()
        return [
            Estimate(
                tenant_id=r["tenant_id"],
                vin=r["vin"],
                pack_id=r["pack_id"],
                session_id=r["session_id"],
                as_of_ms=_ms(r["as_of"]),
                soh_pct=float(r["soh_pct"]),
                ci_low=float(r["ci_low"]),
                ci_high=float(r["ci_high"]),
                obs_soh_pct=float(r["obs_soh_pct"]),
                method=r["method"],
            )
            for r in rows
        ]

    async def ping(self) -> None:
        async with await psycopg.AsyncConnection.connect(self.dsn) as conn:
            await conn.execute("SELECT 1")
