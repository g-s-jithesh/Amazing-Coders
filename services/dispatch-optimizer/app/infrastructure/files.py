"""File adapters: TOML config, tariff reference CSV, and seed master data (for the back-test).

The seed CSVs are the synthetic master data `make seed` generates (data/seed); they are fleet inputs
(models, duties, depots, chargers), not simulator truth.
"""

from __future__ import annotations

import csv
import tomllib
from collections import defaultdict
from pathlib import Path

from app.application.backtest import DepotMaster, VehicleMaster, at_depot
from app.application.planning import DepotState, SolverConfig, VehicleState
from app.domain.degradation import DegradationModel
from app.domain.tariff import TariffWindow


def load_degradation(path: Path) -> DegradationModel:
    with path.open("rb") as f:
        c = tomllib.load(f)
    return DegradationModel(**{k: float(v) for k, v in c.items()})


def load_solver(path: Path) -> SolverConfig:
    with path.open("rb") as f:
        c = tomllib.load(f)
    return SolverConfig(
        budget_s=float(c["budget_s"]),
        max_iter=int(c["max_iter"]),
        patience=int(c["patience"]),
        levels=int(c["levels"]),
        eta_ac=float(c["eta_ac"]),
    )


def load_tariff(path: Path, code: str) -> list[TariffWindow]:
    with path.open(newline="", encoding="utf-8") as f:
        rows = [r for r in csv.DictReader(f) if r["tariff_code"] == code]
    if not rows:
        raise KeyError(f"tariff {code!r} not found in {path}")
    return [
        TariffWindow(
            dow=int(r["dow"]) if r["dow"] else None,
            start_min=int(r["start_min"]),
            end_min=int(r["end_min"]),
            price_paise_per_kwh=int(r["price_paise_per_kwh"]),
        )
        for r in rows
    ]


def _rows(seed: Path, name: str) -> list[dict[str, str]]:
    with (seed / f"{name}.csv").open(newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f))


def load_depots(seed: Path, depot_ids: list[str] | None = None, limit: int | None = None) -> list[DepotMaster]:
    """Depots with their vehicles (by duty depot), duties, models, site cap and connector count.
    SoH is taken as 100 % (nominal capacity): the back-test isolates the scheduling effect."""
    models = {r["code"]: r for r in _rows(seed, "vehicle_model")}
    duty = {r["vehicle_id"]: r for r in _rows(seed, "vehicle_duty")}
    sites = {r["depot_id"]: r for r in _rows(seed, "charger_site") if r["kind"] == "DEPOT"}
    connectors: dict[str, int] = defaultdict(int)
    for c in _rows(seed, "charger"):
        connectors[c["site_id"]] += 1
    by_depot: dict[str, list[VehicleMaster]] = defaultdict(list)
    for v in _rows(seed, "vehicle"):
        d = duty.get(v["id"])
        if d is None:
            continue
        m = models[v["model_code"]]
        by_depot[d["depot_id"]].append(
            VehicleMaster(
                vehicle_id=v["id"],
                nominal_kwh=float(m["nominal_capacity_kwh"]),
                soh_pct=100.0,
                max_ac_kw=float(m["max_ac_kw"]),
                depart_min_ist=int(d["depart_min_ist"]),
                return_min_ist=int(d["return_min_ist"]),
                required_soc_pct=float(d["required_soc_pct"]),
                trip_kwh=float(d["planned_km"]) * float(m["consumption_wh_per_km"]) / 1000.0,
            )
        )
    ids = depot_ids or sorted(i for i in by_depot if i in sites)
    if limit is not None:
        ids = ids[:limit]
    return [
        DepotMaster(
            depot_id=i,
            site_cap_kw=float(sites[i]["site_power_cap_kw"]),
            connectors=connectors[sites[i]["id"]],
            vehicles=sorted(by_depot[i], key=lambda x: x.vehicle_id),
            tenant_id=sites[i]["tenant_id"],
        )
        for i in ids
    ]


class SeedFleet:
    """FleetPort for local runs before fleet-api serves depot snapshots (Step 8): master data from the
    seed CSVs; position (depot or road) from the duty schedule; SoC from a constant.

    ponytail: SoC is a fixed assumption (default 60 %), not live telemetry; the fleet-api snapshot
    adapter replaces this class and carries the live SoC from the stream-processor's Redis state.
    """

    def __init__(self, seed: Path, soc_pct: float = 60.0) -> None:
        self._depots = {d.depot_id: d for d in load_depots(seed)}
        self._soc = soc_pct

    def depot_state(self, tenant_id: str, depot_id: str, now_ms: int) -> DepotState | None:
        d = self._depots.get(depot_id)
        if d is None or d.tenant_id != tenant_id:
            return None
        ist = int(((now_ms + 330 * 60_000) // 60_000) % 1440)
        return DepotState(
            d.depot_id,
            d.site_cap_kw,
            d.connectors,
            [
                VehicleState(
                    v.vehicle_id,
                    v.nominal_kwh,
                    v.soh_pct,
                    v.max_ac_kw,
                    v.depart_min_ist,
                    v.return_min_ist,
                    v.required_soc_pct,
                    self._soc,
                    at_depot(v, ist),
                    v.trip_kwh / v.nominal_kwh * 100.0,
                )
                for v in d.vehicles
            ],
        )
