"""Worker: battery.sessions.v1 → SoH estimates (composition root for the write side).

python -m app.workers.session_consumer      (env: DATABASE_URL, KAFKA_BOOTSTRAP, METRICS_PORT)
"""

from __future__ import annotations

import logging
import os
import signal

import psycopg
from prometheus_client import start_http_server

from app.application.soh import EstimateSoh
from app.domain.soh import ChargeSession
from app.infrastructure.db import PgSohStore
from app.infrastructure.kafka import KafkaRegistry, SessionConsumer
from app.infrastructure.metrics import PromObserver


def main() -> None:
    logging.basicConfig(level=logging.INFO, format='{"level":"%(levelname)s","msg":"%(message)s"}')
    bootstrap = os.environ.get("KAFKA_BOOTSTRAP", "localhost:9092")
    start_http_server(int(os.environ.get("METRICS_PORT", "9104")))

    registry = KafkaRegistry(bootstrap)
    registry.load()
    store = PgSohStore(os.environ["DATABASE_URL"])
    obs = PromObserver()
    use_case = EstimateSoh(registry=registry, store=store, obs=obs)

    def handle(s: ChargeSession) -> None:
        # Permanent data errors would crash-loop on the same offset: count and skip them. Anything else
        # (connection loss) propagates; the batch is not committed and the restarted worker re-reads it.
        try:
            use_case.handle(s)
        except (psycopg.errors.DataError, psycopg.errors.IntegrityError) as e:
            logging.warning("poison session %s: %s", s.session_id, e)
            obs.rejected("poison")

    sessions = SessionConsumer(bootstrap)

    stop = False

    def _stop(*_: object) -> None:
        nonlocal stop
        stop = True

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    logging.info("battery-intel session worker started")
    try:
        while not stop:
            registry.poll(timeout=0)  # follow registry changes (new vehicles, pack swaps)
            sessions.poll_batch(handle, on_poison=lambda: obs.rejected("poison"))
    finally:
        sessions.close()
        registry.close()
        store.close()


if __name__ == "__main__":
    main()
