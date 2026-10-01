from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ChargeState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CHARGE_STATE_UNSPECIFIED: _ClassVar[ChargeState]
    CHARGE_STATE_IDLE: _ClassVar[ChargeState]
    CHARGE_STATE_AC: _ClassVar[ChargeState]
    CHARGE_STATE_DC_FAST: _ClassVar[ChargeState]
    CHARGE_STATE_FAULT: _ClassVar[ChargeState]

class EventType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_TYPE_UNSPECIFIED: _ClassVar[EventType]
    EVENT_TYPE_PERIODIC: _ClassVar[EventType]
    EVENT_TYPE_IGN_ON: _ClassVar[EventType]
    EVENT_TYPE_IGN_OFF: _ClassVar[EventType]
    EVENT_TYPE_PLUG_IN: _ClassVar[EventType]
    EVENT_TYPE_PLUG_OUT: _ClassVar[EventType]
    EVENT_TYPE_HARSH_BRAKE: _ClassVar[EventType]
    EVENT_TYPE_HARSH_ACCEL: _ClassVar[EventType]
    EVENT_TYPE_DTC_RAISED: _ClassVar[EventType]
CHARGE_STATE_UNSPECIFIED: ChargeState
CHARGE_STATE_IDLE: ChargeState
CHARGE_STATE_AC: ChargeState
CHARGE_STATE_DC_FAST: ChargeState
CHARGE_STATE_FAULT: ChargeState
EVENT_TYPE_UNSPECIFIED: EventType
EVENT_TYPE_PERIODIC: EventType
EVENT_TYPE_IGN_ON: EventType
EVENT_TYPE_IGN_OFF: EventType
EVENT_TYPE_PLUG_IN: EventType
EVENT_TYPE_PLUG_OUT: EventType
EVENT_TYPE_HARSH_BRAKE: EventType
EVENT_TYPE_HARSH_ACCEL: EventType
EVENT_TYPE_DTC_RAISED: EventType

class TelemetryEvent(_message.Message):
    __slots__ = ("event_id", "vin", "oem", "ts_event_ms", "ts_ingest_ms", "seq", "lat", "lon", "speed_kmh", "odo_km", "soc_pct", "pack_voltage_v", "pack_current_a", "pack_temp_min_c", "pack_temp_max_c", "cell_v_min_mv", "cell_v_max_mv", "isolation_kohm", "hv_interlock_ok", "aux_12v_v", "ambient_c", "charge_state", "charge_power_kw", "dtc", "evt", "schema_version")
    EVENT_ID_FIELD_NUMBER: _ClassVar[int]
    VIN_FIELD_NUMBER: _ClassVar[int]
    OEM_FIELD_NUMBER: _ClassVar[int]
    TS_EVENT_MS_FIELD_NUMBER: _ClassVar[int]
    TS_INGEST_MS_FIELD_NUMBER: _ClassVar[int]
    SEQ_FIELD_NUMBER: _ClassVar[int]
    LAT_FIELD_NUMBER: _ClassVar[int]
    LON_FIELD_NUMBER: _ClassVar[int]
    SPEED_KMH_FIELD_NUMBER: _ClassVar[int]
    ODO_KM_FIELD_NUMBER: _ClassVar[int]
    SOC_PCT_FIELD_NUMBER: _ClassVar[int]
    PACK_VOLTAGE_V_FIELD_NUMBER: _ClassVar[int]
    PACK_CURRENT_A_FIELD_NUMBER: _ClassVar[int]
    PACK_TEMP_MIN_C_FIELD_NUMBER: _ClassVar[int]
    PACK_TEMP_MAX_C_FIELD_NUMBER: _ClassVar[int]
    CELL_V_MIN_MV_FIELD_NUMBER: _ClassVar[int]
    CELL_V_MAX_MV_FIELD_NUMBER: _ClassVar[int]
    ISOLATION_KOHM_FIELD_NUMBER: _ClassVar[int]
    HV_INTERLOCK_OK_FIELD_NUMBER: _ClassVar[int]
    AUX_12V_V_FIELD_NUMBER: _ClassVar[int]
    AMBIENT_C_FIELD_NUMBER: _ClassVar[int]
    CHARGE_STATE_FIELD_NUMBER: _ClassVar[int]
    CHARGE_POWER_KW_FIELD_NUMBER: _ClassVar[int]
    DTC_FIELD_NUMBER: _ClassVar[int]
    EVT_FIELD_NUMBER: _ClassVar[int]
    SCHEMA_VERSION_FIELD_NUMBER: _ClassVar[int]
    event_id: str
    vin: str
    oem: str
    ts_event_ms: int
    ts_ingest_ms: int
    seq: int
    lat: float
    lon: float
    speed_kmh: float
    odo_km: float
    soc_pct: float
    pack_voltage_v: float
    pack_current_a: float
    pack_temp_min_c: float
    pack_temp_max_c: float
    cell_v_min_mv: int
    cell_v_max_mv: int
    isolation_kohm: float
    hv_interlock_ok: bool
    aux_12v_v: float
    ambient_c: float
    charge_state: ChargeState
    charge_power_kw: float
    dtc: _containers.RepeatedScalarFieldContainer[str]
    evt: EventType
    schema_version: int
    def __init__(self, event_id: _Optional[str] = ..., vin: _Optional[str] = ..., oem: _Optional[str] = ..., ts_event_ms: _Optional[int] = ..., ts_ingest_ms: _Optional[int] = ..., seq: _Optional[int] = ..., lat: _Optional[float] = ..., lon: _Optional[float] = ..., speed_kmh: _Optional[float] = ..., odo_km: _Optional[float] = ..., soc_pct: _Optional[float] = ..., pack_voltage_v: _Optional[float] = ..., pack_current_a: _Optional[float] = ..., pack_temp_min_c: _Optional[float] = ..., pack_temp_max_c: _Optional[float] = ..., cell_v_min_mv: _Optional[int] = ..., cell_v_max_mv: _Optional[int] = ..., isolation_kohm: _Optional[float] = ..., hv_interlock_ok: bool = ..., aux_12v_v: _Optional[float] = ..., ambient_c: _Optional[float] = ..., charge_state: _Optional[_Union[ChargeState, str]] = ..., charge_power_kw: _Optional[float] = ..., dtc: _Optional[_Iterable[str]] = ..., evt: _Optional[_Union[EventType, str]] = ..., schema_version: _Optional[int] = ...) -> None: ...
