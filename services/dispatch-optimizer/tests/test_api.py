from fastapi.testclient import TestClient
from kilowatt.dispatch.v1 import commands_pb2

from app.application.planning import DepotState, VehicleState
from app.domain.plan import InvalidTransition, PlanRecord, Status
from app.infrastructure.kafka import encode
from app.main import create_app

T1, T2 = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
DEPOT = "33333333-3333-4333-8333-333333333333"
NOW = 1788201000000  # 2026-09-01 00:00 IST
DISPATCHER = {"X-Tenant-Id": T1, "X-User-Id": "user-1", "X-Roles": "dispatcher"}


class Fleet:
    def depot_state(self, tenant_id: str, depot_id: str, now_ms: int) -> DepotState | None:
        if tenant_id != T1 or depot_id != DEPOT:
            return None
        vs = [
            VehicleState(f"4444444{i}-4444-4444-8444-444444444444", 45.0, 95.0, 11.0, 540, 1080, 80.0, 30.0, True, 30.0)
            for i in range(5)
        ]
        return DepotState(DEPOT, site_cap_kw=25.0, connectors=3, vehicles=vs)


class MemStore:
    def __init__(self) -> None:
        self.plans: dict[str, PlanRecord] = {}
        self.keys: dict[tuple[str, str], str] = {}
        self.outbox: list[tuple[str, str, bytes]] = []

    def by_idempotency_key(self, tenant_id: str, key: str) -> PlanRecord | None:
        pid = self.keys.get((tenant_id, key))
        return self.plans[pid] if pid else None

    def next_version(self, tenant_id: str, depot_id: str) -> int:
        return 1 + sum(1 for p in self.plans.values() if p.depot_id == depot_id)

    def insert(self, plan: PlanRecord, idempotency_key: str | None) -> None:
        self.plans[plan.id] = plan
        if idempotency_key:
            self.keys[(plan.tenant_id, idempotency_key)] = plan.id

    def get(self, tenant_id: str, plan_id: str) -> PlanRecord | None:
        p = self.plans.get(plan_id)
        return p if p and p.tenant_id == tenant_id else None

    def approve(self, plan: PlanRecord, payload: bytes) -> None:
        for other in self.plans.values():
            if other.depot_id == plan.depot_id and other.version < plan.version:
                other.status = Status.SUPERSEDED
        self.outbox.append(("dispatch.commands.v1", plan.depot_id, payload))


def client(store: MemStore) -> TestClient:
    return TestClient(create_app(fleet=Fleet(), store=store, clock_ms=lambda: NOW, encode=encode))


def test_create_plan_is_idempotent_and_money_adds_up() -> None:
    store = MemStore()
    c = client(store)
    h = {**DISPATCHER, "Idempotency-Key": "k1"}
    r = c.post(f"/internal/v1/depots/{DEPOT}/dispatch-plans", headers=h)
    assert r.status_code == 201
    plan = r.json()
    assert plan["status"] == "DRAFT" and plan["missed_departures"] == 0
    assert plan["energy_cost_paise"] == sum(a["cost_paise"] for a in plan["assignments"])  # exact, integer paise
    assert plan["cost_paise"] == plan["energy_cost_paise"] + plan["degradation_cost_paise"]
    assert plan["saving_paise"] == plan["baseline_cost_paise"] - plan["cost_paise"]
    again = c.post(f"/internal/v1/depots/{DEPOT}/dispatch-plans", headers=h)
    assert again.status_code == 200 and again.json()["id"] == plan["id"] and len(store.plans) == 1


def test_auth_roles_and_tenant_isolation() -> None:
    store = MemStore()
    c = client(store)
    url = f"/internal/v1/depots/{DEPOT}/dispatch-plans"
    assert c.post(url).status_code == 401
    assert c.post(url, headers={**DISPATCHER, "X-Roles": "analyst"}).status_code == 403
    assert c.post(url, headers={**DISPATCHER, "X-Tenant-Id": T2}).status_code == 404  # other tenant's depot
    pid = c.post(url, headers=DISPATCHER).json()["id"]
    other = {**DISPATCHER, "X-Tenant-Id": T2}
    assert c.get(f"/internal/v1/dispatch-plans/{pid}", headers=other).status_code == 404
    assert c.post(f"/internal/v1/dispatch-plans/{pid}/approve", headers=other).status_code == 404
    admin = {**DISPATCHER, "X-Roles": "fleet_admin"}
    assert c.post(f"/internal/v1/dispatch-plans/{pid}/approve", headers=admin).status_code == 403
    assert c.get("/internal/v1/dispatch-plans/not-a-uuid", headers=DISPATCHER).status_code == 404


def test_approve_writes_one_outbox_command_and_supersedes() -> None:
    store = MemStore()
    c = client(store)
    url = f"/internal/v1/depots/{DEPOT}/dispatch-plans"
    first = c.post(url, headers=DISPATCHER).json()["id"]
    second = c.post(url, headers=DISPATCHER).json()["id"]
    r = c.post(f"/internal/v1/dispatch-plans/{second}/approve", headers=DISPATCHER)
    assert r.status_code == 200 and r.json()["status"] == "APPROVED" and r.json()["approved_by"] == "user-1"
    assert c.post(f"/internal/v1/dispatch-plans/{second}/approve", headers=DISPATCHER).status_code == 200
    assert len(store.outbox) == 1  # idempotent approval
    msg = commands_pb2.PlanPublished.FromString(store.outbox[0][2])
    assert msg.plan_id == second and msg.version == 2 and len(msg.assignments) > 0
    assert store.plans[first].status == Status.SUPERSEDED
    assert c.post(f"/internal/v1/dispatch-plans/{first}/approve", headers=DISPATCHER).status_code == 409


def test_transition_rules() -> None:
    from app.domain.plan import transition

    assert transition(Status.DRAFT, Status.APPROVED) == Status.APPROVED
    for cur, tgt in [(Status.PUBLISHED, Status.DRAFT), (Status.SUPERSEDED, Status.APPROVED)]:
        try:
            transition(cur, tgt)
        except InvalidTransition:
            continue
        raise AssertionError(f"{cur} → {tgt} must be refused")


def test_health() -> None:
    c = client(MemStore())
    assert c.get("/healthz").text == "ok" and c.get("/readyz").status_code == 200


def test_depot_list_and_cors() -> None:
    c = client(MemStore())
    assert c.get("/internal/v1/depots").status_code == 401
    assert c.get("/internal/v1/depots", headers=DISPATCHER).json() == {"depots": []}  # the fake fleet lists none
    pre = {"Origin": "null", "Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": "x-tenant-id"}
    assert c.options(f"/internal/v1/depots/{DEPOT}/dispatch-plans", headers=pre).status_code == 200
