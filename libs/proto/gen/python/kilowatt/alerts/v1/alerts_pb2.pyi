from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Severity(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SEVERITY_UNSPECIFIED: _ClassVar[Severity]
    SEVERITY_INFO: _ClassVar[Severity]
    SEVERITY_WARNING: _ClassVar[Severity]
    SEVERITY_HIGH: _ClassVar[Severity]
    SEVERITY_CRITICAL: _ClassVar[Severity]

class AlertState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ALERT_STATE_UNSPECIFIED: _ClassVar[AlertState]
    ALERT_STATE_FIRING: _ClassVar[AlertState]
    ALERT_STATE_RESOLVED: _ClassVar[AlertState]
SEVERITY_UNSPECIFIED: Severity
SEVERITY_INFO: Severity
SEVERITY_WARNING: Severity
SEVERITY_HIGH: Severity
SEVERITY_CRITICAL: Severity
ALERT_STATE_UNSPECIFIED: AlertState
ALERT_STATE_FIRING: AlertState
ALERT_STATE_RESOLVED: AlertState

class Alert(_message.Message):
    __slots__ = ("alert_id", "vin", "tenant_id", "fleet_id", "rule_id", "severity", "state", "window_start_ms", "ts_event_ms", "trigger_ingest_ms", "processed_ms", "evidence", "detail")
    class EvidenceEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: float
        def __init__(self, key: _Optional[str] = ..., value: _Optional[float] = ...) -> None: ...
    ALERT_ID_FIELD_NUMBER: _ClassVar[int]
    VIN_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    FLEET_ID_FIELD_NUMBER: _ClassVar[int]
    RULE_ID_FIELD_NUMBER: _ClassVar[int]
    SEVERITY_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    WINDOW_START_MS_FIELD_NUMBER: _ClassVar[int]
    TS_EVENT_MS_FIELD_NUMBER: _ClassVar[int]
    TRIGGER_INGEST_MS_FIELD_NUMBER: _ClassVar[int]
    PROCESSED_MS_FIELD_NUMBER: _ClassVar[int]
    EVIDENCE_FIELD_NUMBER: _ClassVar[int]
    DETAIL_FIELD_NUMBER: _ClassVar[int]
    alert_id: str
    vin: str
    tenant_id: str
    fleet_id: str
    rule_id: str
    severity: Severity
    state: AlertState
    window_start_ms: int
    ts_event_ms: int
    trigger_ingest_ms: int
    processed_ms: int
    evidence: _containers.ScalarMap[str, float]
    detail: str
    def __init__(self, alert_id: _Optional[str] = ..., vin: _Optional[str] = ..., tenant_id: _Optional[str] = ..., fleet_id: _Optional[str] = ..., rule_id: _Optional[str] = ..., severity: _Optional[_Union[Severity, str]] = ..., state: _Optional[_Union[AlertState, str]] = ..., window_start_ms: _Optional[int] = ..., ts_event_ms: _Optional[int] = ..., trigger_ingest_ms: _Optional[int] = ..., processed_ms: _Optional[int] = ..., evidence: _Optional[_Mapping[str, float]] = ..., detail: _Optional[str] = ...) -> None: ...
