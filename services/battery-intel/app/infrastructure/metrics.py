"""Prometheus metrics (implements the application Observer port)."""

from __future__ import annotations

from prometheus_client import Counter

ESTIMATES = Counter("soh_estimates_total", "Charge sessions turned into SoH estimates.")
DUPLICATES = Counter("soh_duplicate_sessions_total", "Sessions already applied (redelivery); no state change.")
REJECTED = Counter("soh_rejected_sessions_total", "Charge sessions rejected by the quality gate.", ["reason"])


class PromObserver:
    def estimated(self) -> None:
        ESTIMATES.inc()

    def duplicate(self) -> None:
        DUPLICATES.inc()

    def rejected(self, reason: str) -> None:
        REJECTED.labels(reason).inc()
