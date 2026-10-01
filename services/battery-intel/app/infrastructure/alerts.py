"""Recent alerts from alerts.v1 for the console: read the last N records per partition, newest first.

ponytail: reads Kafka directly on each call (stateless, no consumer group, ~10 ms locally). fleet-api
persists alerts to Postgres (Step 8 / F-10) and replaces this with a keyset-paginated inbox.
"""

from __future__ import annotations

from typing import Any

from confluent_kafka import Consumer, TopicPartition
from kilowatt.alerts.v1 import alerts_pb2

TOPIC = "alerts.v1"
SEVERITY = {0: "UNSPECIFIED", 1: "INFO", 2: "WARNING", 3: "HIGH", 4: "CRITICAL"}


def recent_alerts(
    bootstrap: str, tenant_id: str, limit: int = 50, min_severity: int = 2, scan_per_partition: int = 4000
) -> list[dict[str, Any]]:
    c = Consumer({"bootstrap.servers": bootstrap, "group.id": "battery-intel-alerts-peek", "enable.auto.commit": False})
    try:
        parts = list(c.list_topics(TOPIC, timeout=5).topics[TOPIC].partitions)
        tps = []
        for p in parts:
            lo, hi = c.get_watermark_offsets(TopicPartition(TOPIC, p), timeout=5)
            tps.append(TopicPartition(TOPIC, p, max(lo, hi - scan_per_partition)))
        c.assign(tps)
        out: list[dict[str, Any]] = []
        idle = 0
        while idle < 2:
            msgs = c.consume(500, timeout=0.5)
            idle = idle + 1 if not msgs else 0
            for m in msgs:
                raw = m.value()
                if m.error() or raw is None:
                    continue
                a = alerts_pb2.Alert.FromString(raw)
                if a.severity < min_severity:
                    continue
                if a.tenant_id != tenant_id:  # tenant isolation: another tenant's alert is simply absent
                    continue
                out.append(
                    {
                        "alert_id": a.alert_id,
                        "vin": a.vin,
                        "rule_id": a.rule_id,
                        "severity": SEVERITY.get(a.severity, "?"),
                        "state": "FIRING" if a.state == 1 else "RESOLVED",
                        "ts_event_ms": a.ts_event_ms,
                        "detail": a.detail,
                        "latency_ms": (a.processed_ms - a.trigger_ingest_ms) if a.trigger_ingest_ms else None,
                    }
                )
        out.sort(key=lambda x: x["ts_event_ms"], reverse=True)
        return out[:limit]
    finally:
        c.close()
