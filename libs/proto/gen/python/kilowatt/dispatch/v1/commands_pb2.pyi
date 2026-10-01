from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ChargeAssignment(_message.Message):
    __slots__ = ("vehicle_id", "connector_index", "start_ms", "end_ms", "power_kw")
    VEHICLE_ID_FIELD_NUMBER: _ClassVar[int]
    CONNECTOR_INDEX_FIELD_NUMBER: _ClassVar[int]
    START_MS_FIELD_NUMBER: _ClassVar[int]
    END_MS_FIELD_NUMBER: _ClassVar[int]
    POWER_KW_FIELD_NUMBER: _ClassVar[int]
    vehicle_id: str
    connector_index: int
    start_ms: int
    end_ms: int
    power_kw: float
    def __init__(self, vehicle_id: _Optional[str] = ..., connector_index: _Optional[int] = ..., start_ms: _Optional[int] = ..., end_ms: _Optional[int] = ..., power_kw: _Optional[float] = ...) -> None: ...

class PlanPublished(_message.Message):
    __slots__ = ("plan_id", "tenant_id", "depot_id", "version", "method", "horizon_start_ms", "horizon_slots", "approved_by", "approved_at_ms", "assignments")
    PLAN_ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    DEPOT_ID_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    HORIZON_START_MS_FIELD_NUMBER: _ClassVar[int]
    HORIZON_SLOTS_FIELD_NUMBER: _ClassVar[int]
    APPROVED_BY_FIELD_NUMBER: _ClassVar[int]
    APPROVED_AT_MS_FIELD_NUMBER: _ClassVar[int]
    ASSIGNMENTS_FIELD_NUMBER: _ClassVar[int]
    plan_id: str
    tenant_id: str
    depot_id: str
    version: int
    method: str
    horizon_start_ms: int
    horizon_slots: int
    approved_by: str
    approved_at_ms: int
    assignments: _containers.RepeatedCompositeFieldContainer[ChargeAssignment]
    def __init__(self, plan_id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., depot_id: _Optional[str] = ..., version: _Optional[int] = ..., method: _Optional[str] = ..., horizon_start_ms: _Optional[int] = ..., horizon_slots: _Optional[int] = ..., approved_by: _Optional[str] = ..., approved_at_ms: _Optional[int] = ..., assignments: _Optional[_Iterable[_Union[ChargeAssignment, _Mapping]]] = ...) -> None: ...
