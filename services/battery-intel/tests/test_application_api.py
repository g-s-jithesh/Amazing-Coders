from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.application.soh import Estimate, EstimateSoh, PackInfo, Step, soh_report
from app.domain.soh import ChargeSession, KalmanState, Rejection
from app.main import create_app

CATALOGUE = Path(__file__).resolve().parents[3] / "data" / "reference" / "dtc_catalogue.csv"
T1, T2 = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
DAY = 86_400_000


class Reg:
    def pack(self, vin: str) -> PackInfo | None:
        return PackInfo(T1, "pack-1", 100.0) if vin == "V" else None


class MemStore:
    """Behaves like PgSohStore: idempotent on (pack, session); state advances only on insert."""

    def __init__(self) -> None:
        self.state: dict[str, KalmanState] = {}
        self.rows: dict[tuple[str, str], Estimate] = {}

    def apply(self, tenant_id: str, pack_id: str, session_id: str, step: Step) -> Estimate | None:
        st, est = step(self.state.get(pack_id))
        if (pack_id, session_id) in self.rows:
            return None
        self.rows[(pack_id, session_id)] = est
        self.state[pack_id] = st
        return est


class Obs:
    def __init__(self) -> None:
        self.n = {"estimated": 0, "duplicate": 0}
        self.rejections: list[str] = []

    def estimated(self) -> None:
        self.n["estimated"] += 1

    def duplicate(self) -> None:
        self.n["duplicate"] += 1

    def rejected(self, reason: str) -> None:
        self.rejections.append(reason)


def charge(sid: str, end_ms: int, delta_ah: float = -60.0, vin: str = "V") -> ChargeSession:
    return ChargeSession(sid, vin, T1, end_ms - 3_600_000, end_ms, 30, 90, delta_ah, 20, 30, 0, 100, False)


def test_estimate_is_idempotent_per_session() -> None:
    store, obs = MemStore(), Obs()
    uc = EstimateSoh(Reg(), store, obs)
    first = uc.handle(charge("s1", DAY))
    assert isinstance(first, Estimate) and first.soh_pct == pytest.approx(100.0)
    assert uc.handle(charge("s1", DAY)) is None  # redelivery
    assert len(store.rows) == 1 and store.state["pack-1"].n == 1
    second = uc.handle(charge("s2", 2 * DAY, delta_ah=-57.0))
    assert (
        isinstance(second, Estimate) and 95 < second.soh_pct < 100 and second.ci_low < second.soh_pct < second.ci_high
    )
    assert obs.n == {"estimated": 2, "duplicate": 1}


def test_rejections_are_counted() -> None:
    obs = Obs()
    uc = EstimateSoh(Reg(), MemStore(), obs)
    assert uc.handle(charge("s", DAY, vin="UNKNOWN")) is Rejection.UNREGISTERED_VIN
    small = ChargeSession("s2", "V", T1, 0, DAY, 50, 60, -10.0, 20, 30, 0, 100, False)
    assert uc.handle(small) is Rejection.SMALL_DELTA_SOC
    other_tenant = ChargeSession("s3", "V", T2, 0, DAY, 30, 90, -60.0, 20, 30, 0, 100, False)
    assert uc.handle(other_tenant) is Rejection.TENANT_MISMATCH
    assert obs.rejections == ["unregistered_vin", "delta_soc_below_min", "tenant_mismatch"]


def test_report_uses_only_the_current_pack() -> None:
    old = Estimate(T1, "V", "pack-old", "a", DAY, 85.0, 84.0, 86.0, 85.0, "m")
    new = Estimate(T1, "V", "pack-new", "b", 2 * DAY, 100.0, 99.0, 101.0, 100.0, "m")
    rep = soh_report("V", [new, old], 3 * DAY)
    assert rep.latest == new and rep.history == [new]


class FakeReader:
    def __init__(self, rows: dict[str, list[Estimate]]) -> None:
        self.rows = rows

    async def history(self, tenant_id: str, vin: str, limit: int = 500) -> list[Estimate]:
        return [e for e in self.rows.get(vin, []) if e.tenant_id == tenant_id]  # what RLS does

    async def summary(self, tenant_id: str, lowest: int = 20) -> dict[str, object]:
        mine = [e for es in self.rows.values() for e in es if e.tenant_id == tenant_id]
        return {"packs": len(mine), "mean_soh_pct": None, "histogram_5pp": [], "lowest": []}

    async def ping(self) -> None:
        return None


def est(day: int, soh: float) -> Estimate:
    return Estimate(T1, "V", "pack-1", f"s{day}", day * DAY, soh, soh - 1, soh + 1, soh, "coulomb_kalman_v1")


def client() -> TestClient:
    hist = [est(d, 98 - 0.3 * d**0.5) for d in range(0, 120, 10)]
    return TestClient(
        create_app(reader=FakeReader({"V": hist}), catalogue_path=str(CATALOGUE), now_ms=lambda: 120 * DAY)
    )


def test_soh_endpoint_and_tenant_isolation() -> None:
    c = client()
    r = c.get("/internal/v1/vehicles/V/soh", headers={"X-Tenant-Id": T1})
    assert r.status_code == 200
    body = r.json()
    assert body["latest"]["as_of_ms"] == 110 * DAY and len(body["history"]) == 12 and body["rul"]["days"] > 0
    other = c.get("/internal/v1/vehicles/V/soh", headers={"X-Tenant-Id": T2})
    assert other.status_code == 404 and other.headers["content-type"] == "application/problem+json"
    assert c.get("/internal/v1/vehicles/V/soh").status_code == 401
    assert c.get("/internal/v1/vehicles/V/soh", headers={"X-Tenant-Id": "not-a-uuid"}).status_code == 401


def test_dtc_endpoint_and_health() -> None:
    c = client()
    r = c.get("/internal/v1/dtc/P0A7E")
    assert (
        r.status_code == 200 and r.json()["runbook_id"] == "RB-THERMAL-01" and r.json()["source_status"] == "unverified"
    )
    bad = c.get("/internal/v1/dtc/PX12Z")
    assert bad.status_code == 400 and bad.json()["title"] == "Invalid DTC"
    assert c.get("/healthz").text == "ok" and c.get("/readyz").text == "ready"
    assert c.get("/metrics").status_code == 200


def test_report_without_history() -> None:
    rep = soh_report("V", [], 0)
    assert rep.latest is None and rep.rul is None


def test_summary_and_alerts_are_tenant_scoped() -> None:
    seen: list[tuple[str, int]] = []

    def alerts(tenant: str, limit: int) -> list[dict[str, object]]:
        seen.append((tenant, limit))
        return [{"vin": "V", "rule_id": "DTC_RAISED"}] if tenant == T1 else []

    c = TestClient(
        create_app(
            reader=FakeReader({"V": [est(1, 90.0)]}),
            catalogue_path=str(CATALOGUE),
            now_ms=lambda: DAY,
            alerts_fn=alerts,
        )
    )
    assert c.get("/internal/v1/fleet/soh-summary", headers={"X-Tenant-Id": T1}).json()["packs"] == 1
    assert c.get("/internal/v1/fleet/soh-summary", headers={"X-Tenant-Id": T2}).json()["packs"] == 0
    assert c.get("/internal/v1/fleet/soh-summary").status_code == 401
    assert c.get("/internal/v1/alerts/recent?limit=999", headers={"X-Tenant-Id": T1}).json()["alerts"][0]["vin"] == "V"
    assert seen == [(T1, 200)]  # limit clamped
    assert c.get("/internal/v1/alerts/recent", headers={"X-Tenant-Id": T2}).json() == {"alerts": []}
    assert c.get("/internal/v1/alerts/recent").status_code == 401
    preflight = {"Origin": "null", "Access-Control-Request-Method": "GET"}
    assert c.options("/internal/v1/dtc/P0A7E", headers=preflight).status_code == 200
