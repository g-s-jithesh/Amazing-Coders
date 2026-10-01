"""Worker: dispatch.outbox → Kafka dispatch.commands.v1 (transactional outbox relay).

python -m app.workers.outbox_relay      (env: RELAY_DATABASE_URL as dispatch_relay, KAFKA_BOOTSTRAP)

At-least-once: rows are locked, published, acknowledged, then marked published in the same transaction;
a crash in between re-publishes them (consumers dedupe by plan_id).
"""

from __future__ import annotations

import logging
import os
import signal
import time

from app.infrastructure.db import PgOutbox
from app.infrastructure.kafka import CommandProducer


def main() -> None:
    logging.basicConfig(level=logging.INFO, format='{"level":"%(levelname)s","msg":"%(message)s"}')
    outbox = PgOutbox(os.environ["RELAY_DATABASE_URL"])
    producer = CommandProducer(os.environ.get("KAFKA_BOOTSTRAP", "localhost:9092"))
    stop = False

    def _stop(*_: object) -> None:
        nonlocal stop
        stop = True

    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    logging.info("outbox relay started")
    while not stop:
        rows = outbox.claim()
        if not rows:
            outbox.rollback()
            time.sleep(0.5)
            continue
        try:
            producer.send_all([(r["topic"], r["key"], bytes(r["payload"])) for r in rows])
        except Exception:
            logging.exception("publish failed; will retry")
            outbox.rollback()
            time.sleep(2)
            continue
        outbox.mark_published([r["id"] for r in rows], [r["plan_id"] for r in rows])
        logging.info("published %d plan command(s)", len(rows))
    outbox.close()


if __name__ == "__main__":
    main()
