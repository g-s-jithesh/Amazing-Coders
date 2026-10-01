"""dispatch.commands.v1 contract (Protobuf) and the outbox relay's producer."""

from __future__ import annotations

import logging

from confluent_kafka import Producer
from kilowatt.dispatch.v1 import commands_pb2

from app.domain.plan import PlanRecord

log = logging.getLogger(__name__)


def encode(plan: PlanRecord) -> bytes:
    msg = commands_pb2.PlanPublished(
        plan_id=plan.id,
        tenant_id=plan.tenant_id,
        depot_id=plan.depot_id,
        version=plan.version,
        method=plan.method,
        horizon_start_ms=plan.horizon_start_ms,
        horizon_slots=plan.horizon_slots,
        approved_by=plan.approved_by or "",
        approved_at_ms=plan.approved_at_ms or 0,
        assignments=[
            commands_pb2.ChargeAssignment(
                vehicle_id=a.vehicle_id,
                connector_index=a.connector_index,
                start_ms=a.start_ms,
                end_ms=a.end_ms,
                power_kw=a.power_kw,
            )
            for a in plan.assignments
        ],
    )
    return msg.SerializeToString()


class CommandProducer:
    """Idempotent producer, acks=all (root CLAUDE.md §3.3). ``send_all`` returns only when every record
    is acknowledged, or raises: the relay then rolls back and the rows are retried (at-least-once)."""

    def __init__(self, bootstrap: str) -> None:
        self._p = Producer({"bootstrap.servers": bootstrap, "enable.idempotence": True, "acks": "all"})

    def send_all(self, records: list[tuple[str, str, bytes]], timeout_s: float = 10.0) -> None:
        errors: list[str] = []

        def done(err: object, _msg: object) -> None:
            if err is not None:
                errors.append(str(err))

        for topic, key, value in records:
            self._p.produce(topic, key=key.encode(), value=value, on_delivery=done)
        left = self._p.flush(timeout_s)
        if left or errors:
            raise RuntimeError(f"publish failed: {left} unflushed, errors={errors[:3]}")
