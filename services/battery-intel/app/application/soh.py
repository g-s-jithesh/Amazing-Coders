"""Use cases: estimate SoH from a charge session; read a vehicle's SoH history with CI and RUL.

Depends only on ports. Idempotency: an estimate is keyed by (pack_id, session_id); the Kalman state
advances only when that insert actually happened, so a redelivered session changes nothing.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Protocol

from app.domain.soh import (
    ChargeSession,
    KalmanState,
    Observation,
    Rejection,
    Rul,
    fit_rul,
    kalman_update,
    observe,
)

METHOD = "coulomb_kalman_v1"


@dataclass(frozen=True, slots=True)
class PackInfo:
    tenant_id: str
    pack_id: str
    nominal_ah: float


@dataclass(frozen=True, slots=True)
class Estimate:
    tenant_id: str
    vin: str
    pack_id: str
    session_id: str
    as_of_ms: int
    soh_pct: float
    ci_low: float
    ci_high: float
    obs_soh_pct: float
    method: str


type Step = Callable[[KalmanState | None], tuple[KalmanState, Estimate]]


class Registry(Protocol):
    def pack(self, vin: str) -> PackInfo | None: ...


class SohStore(Protocol):
    """Transactional store. ``apply`` must run in one transaction under the estimate's tenant:
    load the pack's Kalman state FOR UPDATE, call ``step``, insert the estimate (ON CONFLICT DO NOTHING),
    and save the new state only if the insert happened. It returns the stored estimate, or None
    when the session was already applied."""

    def apply(self, tenant_id: str, pack_id: str, session_id: str, step: Step) -> Estimate | None: ...


class Observer(Protocol):
    def estimated(self) -> None: ...
    def duplicate(self) -> None: ...
    def rejected(self, reason: str) -> None: ...


@dataclass
class EstimateSoh:
    registry: Registry
    store: SohStore
    obs: Observer

    def handle(self, s: ChargeSession) -> Estimate | Rejection | None:
        """Returns the new estimate, the rejection reason, or None for an already-applied session."""
        info = self.registry.pack(s.vin)
        if info is None:
            self.obs.rejected(Rejection.UNREGISTERED_VIN.value)
            return Rejection.UNREGISTERED_VIN
        if s.tenant_id and s.tenant_id != info.tenant_id:  # defence in depth: stream and registry must agree
            self.obs.rejected(Rejection.TENANT_MISMATCH.value)
            return Rejection.TENANT_MISMATCH
        result = observe(s, info.nominal_ah)
        if isinstance(result, Rejection):
            self.obs.rejected(result.value)
            return result
        o: Observation = result

        def step(prev: KalmanState | None) -> tuple[KalmanState, Estimate]:
            st = kalman_update(prev, o, s.end_ms)
            lo, hi = st.ci95()
            return st, Estimate(
                tenant_id=info.tenant_id,
                vin=s.vin,
                pack_id=info.pack_id,
                session_id=s.session_id,
                as_of_ms=s.end_ms,
                soh_pct=st.soh_pct,
                ci_low=lo,
                ci_high=hi,
                obs_soh_pct=o.soh_pct,
                method=METHOD,
            )

        est = self.store.apply(info.tenant_id, info.pack_id, s.session_id, step)
        if est is None:
            self.obs.duplicate()
        else:
            self.obs.estimated()
        return est


@dataclass(frozen=True, slots=True)
class SohReport:
    vin: str
    history: list[Estimate]
    latest: Estimate | None
    rul: Rul | None


def soh_report(vin: str, history: list[Estimate], now_ms: int) -> SohReport:
    history = sorted(history, key=lambda e: e.as_of_ms)
    if history:  # after a pack swap only the current pack's series is meaningful
        history = [e for e in history if e.pack_id == history[-1].pack_id]
    latest = history[-1] if history else None
    rul = fit_rul([(e.as_of_ms, e.soh_pct) for e in history], now_ms) if history else None
    return SohReport(vin=vin, history=history, latest=latest, rul=rul)
