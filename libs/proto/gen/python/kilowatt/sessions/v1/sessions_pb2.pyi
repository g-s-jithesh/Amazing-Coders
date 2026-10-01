from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Kind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    KIND_UNSPECIFIED: _ClassVar[Kind]
    KIND_CHARGE: _ClassVar[Kind]
    KIND_DRIVE: _ClassVar[Kind]

class Phase(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PHASE_UNSPECIFIED: _ClassVar[Phase]
    PHASE_START: _ClassVar[Phase]
    PHASE_END: _ClassVar[Phase]
KIND_UNSPECIFIED: Kind
KIND_CHARGE: Kind
KIND_DRIVE: Kind
PHASE_UNSPECIFIED: Phase
PHASE_START: Phase
PHASE_END: Phase

class Session(_message.Message):
    __slots__ = ("session_id", "vin", "tenant_id", "kind", "phase", "start_ms", "end_ms", "soc_start_pct", "soc_end_pct", "delta_ah", "delta_kwh", "v_rest_before", "temp_min_c", "temp_max_c", "max_c_rate", "charger_type", "distance_km", "gaps", "samples")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    VIN_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    PHASE_FIELD_NUMBER: _ClassVar[int]
    START_MS_FIELD_NUMBER: _ClassVar[int]
    END_MS_FIELD_NUMBER: _ClassVar[int]
    SOC_START_PCT_FIELD_NUMBER: _ClassVar[int]
    SOC_END_PCT_FIELD_NUMBER: _ClassVar[int]
    DELTA_AH_FIELD_NUMBER: _ClassVar[int]
    DELTA_KWH_FIELD_NUMBER: _ClassVar[int]
    V_REST_BEFORE_FIELD_NUMBER: _ClassVar[int]
    TEMP_MIN_C_FIELD_NUMBER: _ClassVar[int]
    TEMP_MAX_C_FIELD_NUMBER: _ClassVar[int]
    MAX_C_RATE_FIELD_NUMBER: _ClassVar[int]
    CHARGER_TYPE_FIELD_NUMBER: _ClassVar[int]
    DISTANCE_KM_FIELD_NUMBER: _ClassVar[int]
    GAPS_FIELD_NUMBER: _ClassVar[int]
    SAMPLES_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    vin: str
    tenant_id: str
    kind: Kind
    phase: Phase
    start_ms: int
    end_ms: int
    soc_start_pct: float
    soc_end_pct: float
    delta_ah: float
    delta_kwh: float
    v_rest_before: float
    temp_min_c: float
    temp_max_c: float
    max_c_rate: float
    charger_type: str
    distance_km: float
    gaps: int
    samples: int
    def __init__(self, session_id: _Optional[str] = ..., vin: _Optional[str] = ..., tenant_id: _Optional[str] = ..., kind: _Optional[_Union[Kind, str]] = ..., phase: _Optional[_Union[Phase, str]] = ..., start_ms: _Optional[int] = ..., end_ms: _Optional[int] = ..., soc_start_pct: _Optional[float] = ..., soc_end_pct: _Optional[float] = ..., delta_ah: _Optional[float] = ..., delta_kwh: _Optional[float] = ..., v_rest_before: _Optional[float] = ..., temp_min_c: _Optional[float] = ..., temp_max_c: _Optional[float] = ..., max_c_rate: _Optional[float] = ..., charger_type: _Optional[str] = ..., distance_km: _Optional[float] = ..., gaps: _Optional[int] = ..., samples: _Optional[int] = ...) -> None: ...
