"""Kafka adapters (confluent-kafka): the fleet.vehicle.v1 registry (ADR-0006) and the
battery.sessions.v1 consumer. Protobuf contracts come from libs/proto (kilowatt-proto)."""

from __future__ import annotations

import logging
import threading
import time
from collections.abc import Callable

from confluent_kafka import OFFSET_BEGINNING, Consumer, KafkaError, Message, TopicPartition
from google.protobuf.message import DecodeError
from kilowatt.fleet.v1 import vehicle_pb2
from kilowatt.sessions.v1 import sessions_pb2

from app.application.soh import PackInfo
from app.domain.soh import ChargeSession

log = logging.getLogger(__name__)

REGISTRY_TOPIC = "fleet.vehicle.v1"
SESSIONS_TOPIC = "battery.sessions.v1"
GROUP = "battery-intel"


class KafkaRegistry:
    """VIN → pack registry, read from the compacted topic from the beginning and then followed."""

    def __init__(self, bootstrap: str) -> None:
        self._c = Consumer(
            {"bootstrap.servers": bootstrap, "group.id": "battery-intel-registry-unused", "enable.auto.commit": False}
        )
        md = self._c.list_topics(REGISTRY_TOPIC, timeout=10)
        parts = list(md.topics[REGISTRY_TOPIC].partitions)
        self._targets = {
            p: self._c.get_watermark_offsets(TopicPartition(REGISTRY_TOPIC, p), timeout=10)[1] for p in parts
        }
        self._pos = {p: 0 for p in parts}
        self._c.assign([TopicPartition(REGISTRY_TOPIC, p, OFFSET_BEGINNING) for p in parts])
        self._packs: dict[str, PackInfo] = {}
        self._lock = threading.Lock()

    def pack(self, vin: str) -> PackInfo | None:
        with self._lock:
            return self._packs.get(vin)

    def __len__(self) -> int:
        with self._lock:
            return len(self._packs)

    def caught_up(self) -> bool:
        return all(self._pos[p] >= end for p, end in self._targets.items())

    def poll(self, timeout: float = 0.0, max_records: int = 10_000) -> int:
        n = 0
        for msg in self._c.consume(num_messages=max_records, timeout=timeout):
            if msg.error():
                continue
            self._pos[msg.partition()] = (msg.offset() or 0) + 1
            key = (msg.key() or b"").decode()
            value = msg.value()
            with self._lock:
                if value is None:
                    self._packs.pop(key, None)
                    continue
                v = vehicle_pb2.Vehicle.FromString(value)
                if v.current_pack_id and v.capacity_kwh > 0 and v.nominal_voltage_v > 0:
                    self._packs[v.vin] = PackInfo(
                        tenant_id=v.tenant_id,
                        pack_id=v.current_pack_id,
                        nominal_ah=v.capacity_kwh * 1000 / v.nominal_voltage_v,
                    )
            n += 1
        return n

    def load(self, deadline_s: float = 60.0) -> None:
        end = time.monotonic() + deadline_s
        while not self.caught_up() and time.monotonic() < end:
            self.poll(timeout=0.5)
        log.info("registry loaded: %d packs, caught_up=%s", len(self), self.caught_up())
        if len(self) == 0:
            log.warning("registry is empty: every session will be rejected as unregistered_vin (run make seed)")

    def close(self) -> None:
        self._c.close()


def to_session(m: sessions_pb2.Session) -> ChargeSession | None:
    """Charge-session END records only; everything else is ignored by SoH estimation."""
    if m.kind != sessions_pb2.KIND_CHARGE or m.phase != sessions_pb2.PHASE_END:
        return None
    return ChargeSession(
        session_id=m.session_id,
        vin=m.vin,
        tenant_id=m.tenant_id,
        start_ms=m.start_ms,
        end_ms=m.end_ms,
        soc_start_pct=m.soc_start_pct,
        soc_end_pct=m.soc_end_pct,
        delta_ah=m.delta_ah,
        temp_min_c=m.temp_min_c,
        temp_max_c=m.temp_max_c,
        gaps=m.gaps,
        samples=m.samples,
        rested_before=m.HasField("v_rest_before"),
    )


class SessionConsumer:
    """At-least-once: offsets are committed after the handler returned (the store is idempotent)."""

    def __init__(self, bootstrap: str) -> None:
        self._c = Consumer(
            {
                "bootstrap.servers": bootstrap,
                "group.id": GROUP,
                # At-least-once without blocking: an offset is *stored* only after its record was handled,
                # and the client's background thread commits stored offsets. A synchronous commit() can
                # block forever while the group coordinator is unavailable (seen locally under load).
                "enable.auto.commit": True,
                "enable.auto.offset.store": False,
                "auto.commit.interval.ms": 1000,
                "auto.offset.reset": "earliest",
            }
        )
        self._c.subscribe([SESSIONS_TOPIC])

    def poll_batch(
        self, handle: Callable[[ChargeSession], object], on_poison: Callable[[], None], timeout: float = 0.5
    ) -> int:
        """Undecodable records are counted via on_poison and skipped (committed): retrying cannot fix them.
        A handler exception propagates before the record's offset is stored, so it is redelivered."""
        msgs: list[Message] = self._c.consume(num_messages=500, timeout=timeout)
        n = 0
        for msg in msgs:
            err = msg.error()
            if err is not None:
                if err.code() != KafkaError._PARTITION_EOF:
                    log.warning("sessions fetch error: %s", err)
                continue
            raw = msg.value()
            s = None
            if raw is not None:
                try:
                    s = to_session(sessions_pb2.Session.FromString(raw))
                except DecodeError:
                    log.warning("undecodable session at %s/%s", msg.partition(), msg.offset())
                    on_poison()
            if s is not None:
                handle(s)
                n += 1
            self._c.store_offsets(message=msg)
        return n

    def close(self) -> None:
        self._c.close()
