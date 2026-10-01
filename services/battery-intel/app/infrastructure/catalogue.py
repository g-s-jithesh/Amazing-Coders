"""Loads data/reference/dtc_catalogue.csv (the same file the stream-processor uses for severities)."""

from __future__ import annotations

import csv
from pathlib import Path

from app.domain.dtc import CatalogueEntry


def load(path: str | Path) -> dict[str, CatalogueEntry]:
    with open(path, newline="", encoding="utf-8") as f:
        return {
            r["code"]: CatalogueEntry(
                code=r["code"],
                system=r["system"],
                severity=r["severity"],
                description=r["description"],
                sim_fault=r["sim_fault"],
                runbook_id=r["runbook_id"],
                source_status=r["source_status"],
            )
            for r in csv.DictReader(f)
        }
