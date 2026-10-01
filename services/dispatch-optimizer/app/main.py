"""dispatch-optimizer internal API (composition root). Errors are RFC 9457 problem+json.

ponytail: caller identity (tenant, user subject, roles) comes from X-Tenant-Id / X-User-Id / X-Roles set
by the trusted caller (fleet-api, copilot) until F-10 brings JWT passthrough; RLS already enforces the
tenant in the database. Plans are solved synchronously within the solver budget (2 s); move to a job
queue if depots outgrow it.
"""

from __future__ import annotations

import os
import time
import uuid
from collections.abc import Callable
from dataclasses import asdict
from pathlib import Path
from typing import Annotated, Any

from fastapi import FastAPI, Header, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, PlainTextResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from app.application.plans import FleetPort, NotFound, Plans, PlanStore
from app.domain.plan import InvalidTransition, PlanRecord
from app.infrastructure.files import SeedFleet, load_degradation, load_solver, load_tariff

PROBLEM = "application/problem+json"
SVC = Path(__file__).resolve().parents[1]
CREATE_ROLES = {"dispatcher", "fleet_admin"}
APPROVE_ROLES = {"dispatcher"}


def problem(status: int, title: str, detail: str, instance: str) -> JSONResponse:
    return JSONResponse(
        {"type": "about:blank", "title": title, "status": status, "detail": detail, "instance": instance},
        status_code=status,
        media_type=PROBLEM,
    )


def _uuid(x: str | None) -> str | None:
    try:
        return str(uuid.UUID(x or ""))
    except ValueError:
        return None


def plan_json(p: PlanRecord, with_assignments: bool = True) -> dict[str, Any]:
    d = asdict(p)
    d["status"] = p.status.value
    d["saving_paise"] = p.baseline_cost_paise - p.cost_paise
    if not with_assignments:
        d.pop("assignments")
    return d


def create_app(
    fleet: FleetPort | None = None,
    store: PlanStore | None = None,
    clock_ms: Callable[[], int] | None = None,
    encode: Callable[[PlanRecord], bytes] | None = None,
) -> FastAPI:
    app = FastAPI(title="dispatch-optimizer", version="0.1.0", docs_url="/docs")
    # ponytail: any origin may call this internal API so the static console works from file://; the
    # tenant guard is RLS + the caller headers, and F-10 puts JWT + a CORS allow-list in front.
    app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["GET", "POST"], allow_headers=["*"])
    root = Path(os.environ.get("KILOWATT_ROOT", SVC.parents[1]))
    if store is None:
        from app.infrastructure.db import PgPlanStore

        store = PgPlanStore(os.environ.get("DATABASE_URL", ""))
    if encode is None:
        from app.infrastructure.kafka import encode as proto_encode

        encode = proto_encode
    tariff_code = os.environ.get("TARIFF_CODE", "DEPOT_TOD_SYNTH")
    plans = Plans(
        fleet=fleet or SeedFleet(Path(os.environ.get("SEED_DIR", str(root / "data" / "seed")))),
        store=store,
        tariff=load_tariff(
            Path(os.environ.get("TARIFFS", str(root / "data" / "reference" / "tariffs.csv"))), tariff_code
        ),
        tariff_code=tariff_code,
        model=load_degradation(SVC / "config" / "degradation.toml"),
        cfg=load_solver(SVC / "config" / "solver.toml"),
        encode=encode,
        clock_ms=clock_ms or (lambda: int(time.time() * 1000)),
    )

    def caller(tenant: str | None, user: str | None, roles: str | None) -> tuple[str, str, set[str]] | None:
        t = _uuid(tenant)
        if t is None or not user:
            return None
        return t, user, {r.strip() for r in (roles or "").split(",") if r.strip()}

    @app.get("/healthz", response_class=PlainTextResponse)
    def healthz() -> str:
        return "ok"

    @app.get("/readyz", response_class=PlainTextResponse)
    def readyz(request: Request) -> Any:
        try:
            ping = getattr(store, "ping", None)
            if ping:
                ping()
        except Exception as e:  # noqa: BLE001 - readiness reports any dependency failure
            return problem(503, "Not ready", str(e), request.url.path)
        return "ready"

    @app.get("/metrics")
    def metrics() -> PlainTextResponse:
        return PlainTextResponse(generate_latest(), media_type=CONTENT_TYPE_LATEST)

    @app.get("/internal/v1/depots")
    def list_depots(
        request: Request,
        x_tenant_id: Annotated[str | None, Header()] = None,
        x_user_id: Annotated[str | None, Header()] = None,
        x_roles: Annotated[str | None, Header()] = None,
    ) -> Any:
        who = caller(x_tenant_id, x_user_id, x_roles)
        if who is None:
            return problem(401, "Unauthenticated", "missing or invalid caller identity", request.url.path)
        lister = getattr(plans.fleet, "list_depots", None)
        return {"depots": lister(who[0]) if lister else []}

    @app.post("/internal/v1/depots/{depot_id}/dispatch-plans")
    def create_plan(
        depot_id: str,
        request: Request,
        x_tenant_id: Annotated[str | None, Header()] = None,
        x_user_id: Annotated[str | None, Header()] = None,
        x_roles: Annotated[str | None, Header()] = None,
        idempotency_key: Annotated[str | None, Header()] = None,
    ) -> Any:
        who = caller(x_tenant_id, x_user_id, x_roles)
        if who is None:
            return problem(401, "Unauthenticated", "missing or invalid caller identity", request.url.path)
        tenant, user, roles = who
        if not roles & CREATE_ROLES:
            return problem(403, "Forbidden", "requires dispatcher or fleet_admin", request.url.path)
        depot = _uuid(depot_id)
        if depot is None:
            return problem(404, "Not found", f"depot {depot_id}", request.url.path)
        try:
            plan, created = plans.create(tenant, depot, user, idempotency_key)
        except NotFound:
            return problem(404, "Not found", f"depot {depot_id}", request.url.path)
        return JSONResponse(plan_json(plan), status_code=201 if created else 200)

    @app.get("/internal/v1/dispatch-plans/{plan_id}")
    def get_plan(
        plan_id: str,
        request: Request,
        x_tenant_id: Annotated[str | None, Header()] = None,
        x_user_id: Annotated[str | None, Header()] = None,
        x_roles: Annotated[str | None, Header()] = None,
    ) -> Any:
        who = caller(x_tenant_id, x_user_id, x_roles)
        if who is None:
            return problem(401, "Unauthenticated", "missing or invalid caller identity", request.url.path)
        pid = _uuid(plan_id)
        try:
            if pid is None:
                raise NotFound(plan_id)
            return plan_json(plans.get(who[0], pid))
        except NotFound:  # also another tenant's plan: 404, never 403 (no enumeration)
            return problem(404, "Not found", f"plan {plan_id}", request.url.path)

    @app.post("/internal/v1/dispatch-plans/{plan_id}/approve")
    def approve_plan(
        plan_id: str,
        request: Request,
        x_tenant_id: Annotated[str | None, Header()] = None,
        x_user_id: Annotated[str | None, Header()] = None,
        x_roles: Annotated[str | None, Header()] = None,
    ) -> Any:
        who = caller(x_tenant_id, x_user_id, x_roles)
        if who is None:
            return problem(401, "Unauthenticated", "missing or invalid caller identity", request.url.path)
        tenant, user, roles = who
        pid = _uuid(plan_id)
        try:
            if pid is None:
                raise NotFound(plan_id)
            if not roles & APPROVE_ROLES:
                plans.get(tenant, pid)  # 404 before 403: don't confirm another tenant's plan exists
                return problem(403, "Forbidden", "only a dispatcher can approve a plan", request.url.path)
            return plan_json(plans.approve(tenant, pid, user), with_assignments=False)
        except NotFound:
            return problem(404, "Not found", f"plan {plan_id}", request.url.path)
        except InvalidTransition as e:
            return problem(409, "Conflict", str(e), request.url.path)

    return app


def app() -> FastAPI:  # uvicorn --factory app.main:app
    return create_app()
