from kilowatt.sessions.v1 import sessions_pb2 as pb

from app.infrastructure.kafka import to_session


def session(kind: int, phase: int, **kw: object) -> pb.Session:
    return pb.Session(session_id="s", vin="V", tenant_id="t", kind=kind, phase=phase, start_ms=1, end_ms=2, **kw)  # type: ignore[arg-type]


def test_only_charge_end_records_become_sessions() -> None:
    assert to_session(session(pb.KIND_CHARGE, pb.PHASE_START)) is None
    assert to_session(session(pb.KIND_DRIVE, pb.PHASE_END)) is None
    s = to_session(session(pb.KIND_CHARGE, pb.PHASE_END, soc_start_pct=30, soc_end_pct=80, delta_ah=-50, samples=12))
    assert s is not None and (s.soc_start_pct, s.soc_end_pct, s.delta_ah, s.samples) == (30, 80, -50, 12)
    assert s.rested_before is False


def test_rest_flag_follows_field_presence_not_value() -> None:
    s = to_session(session(pb.KIND_CHARGE, pb.PHASE_END, v_rest_before=0.0))
    assert s is not None and s.rested_before is True
