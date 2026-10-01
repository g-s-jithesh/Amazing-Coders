"""battery-intel internal API (composition root for the read side).

Internal endpoints are called by fleet-api / copilot-agent. Errors are RFC 9457 problem+json.
ponytail: the tenant comes from the X-Tenant-Id header set by the trusted caller; JWT verification
(service token + user token passthrough) arrives with Keycloak in F-10. RLS is already enforced in the DB.
"""

from __future__ import annotations

import os
import time
import uuid
from dataclasses import asdict
from typing import Annotated, Any

from fastapi import FastAPI, Header, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, PlainTextResponse
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

from app.application.soh import METHOD, SohReport, soh_report
from app.domain.dtc import InvalidCode, decode
from app.infrastructure.catalogue import load as load_catalogue
from app.infrastructure.db import PgSohReader

PROBLEM = "application/problem+json"


def problem(status: int, title: str, detail: str, instance: str) -> JSONResponse:
    return JSONResponse(
        {"type": "about:blank", "title": title, "status": status, "detail": detail, "instance": instance},
        status_code=status,
        media_type=PROBLEM,
    )


def create_app(
    reader: Any = None, catalogue_path: str | None = None, now_ms: Any = None, alerts_fn: Any = None
) -> FastAPI:
    app = FastAPI(title="battery-intel", version="0.1.0", docs_url="/docs")
    # ponytail: any origin may call this internal API so the static console works from file://; the
    # tenant guard is RLS, and F-10 puts JWT + a CORS allow-list in front.
    app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["GET"], allow_headers=["*"])
    kafka = os.environ.get("KAFKA_BOOTSTRAP", "localhost:9092")

    def get_alerts(tenant: str, limit: int, min_severity: int) -> list[dict[str, Any]]:
        if alerts_fn is not None:
            return list(alerts_fn(tenant, limit))
        from app.infrastructure.alerts import recent_alerts

        return recent_alerts(kafka, tenant, limit, min_severity)

    rd = reader or PgSohReader(os.environ.get("DATABASE_URL", ""))
    cat = load_catalogue(catalogue_path or os.environ.get("DTC_CATALOGUE", "data/reference/dtc_catalogue.csv"))
    clock = now_ms or (lambda: int(time.time() * 1000))

    def tenant_or_none(tenant: str | None) -> str | None:
        try:
            return str(uuid.UUID(tenant or ""))
        except ValueError:
            return None

    @app.get("/healthz", response_class=PlainTextResponse)
    async def healthz() -> str:
        return "ok"

    @app.get("/readyz", response_class=PlainTextResponse)
    async def readyz(request: Request) -> Any:
        try:
            await rd.ping()
        except Exception as e:  # noqa: BLE001 - readiness reports any dependency failure
            return problem(503, "Not ready", str(e), str(request.url.path))
        return "ready"

    @app.get("/metrics")
    async def metrics() -> PlainTextResponse:
        return PlainTextResponse(generate_latest(), media_type=CONTENT_TYPE_LATEST)

    @app.get("/internal/v1/vehicles/{vin}/soh")
    async def vehicle_soh(vin: str, request: Request, x_tenant_id: Annotated[str | None, Header()] = None) -> Any:
        tenant = tenant_or_none(x_tenant_id)
        if tenant is None:
            return problem(401, "Unauthenticated", "missing or invalid tenant", request.url.path)
        rep: SohReport = soh_report(vin, await rd.history(tenant, vin), clock())
        if rep.latest is None:  # also what another tenant's VIN looks like: 404, never 403 (no enumeration)
            return problem(404, "Not found", f"no SoH estimates for {vin}", request.url.path)
        return {
            "vin": vin,
            "method": METHOD,
            "latest": asdict(rep.latest),
            "rul": asdict(rep.rul) if rep.rul else None,
            "history": [asdict(e) for e in rep.history],
        }

    @app.get("/internal/v1/fleet/soh-summary")
    async def soh_summary(request: Request, x_tenant_id: Annotated[str | None, Header()] = None) -> Any:
        tenant = tenant_or_none(x_tenant_id)
        if tenant is None:
            return problem(401, "Unauthenticated", "missing or invalid tenant", request.url.path)
        return await rd.summary(tenant)

    @app.get("/internal/v1/alerts/recent")
    async def alerts_recent(
        request: Request,
        limit: int = 50,
        min_severity: str = "WARNING",
        x_tenant_id: Annotated[str | None, Header()] = None,
    ) -> Any:
        tenant = tenant_or_none(x_tenant_id)
        if tenant is None:
            return problem(401, "Unauthenticated", "missing or invalid tenant", request.url.path)
        floor = {"INFO": 1, "WARNING": 2, "HIGH": 3, "CRITICAL": 4}.get(min_severity.upper(), 2)
        return {"alerts": await run_in_threadpool(get_alerts, tenant, max(1, min(limit, 200)), floor)}

    @app.get("/internal/v1/dtc/{code}")
    async def dtc(code: str, request: Request) -> Any:
        try:
            return asdict(decode(code, cat))
        except InvalidCode:
            return problem(400, "Invalid DTC", f"{code!r} is not an SAE J2012 code", request.url.path)

    return app


def app() -> FastAPI:  # uvicorn --factory app.main:app
    return create_app()
