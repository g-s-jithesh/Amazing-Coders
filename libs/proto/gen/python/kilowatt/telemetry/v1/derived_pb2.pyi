from kilowatt.telemetry.v1 import telemetry_pb2 as _telemetry_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Rollup1m(_message.Message):
    __slots__ = ("vin", "minute_start_ms", "count", "soc_min_pct", "soc_max_pct", "soc_avg_pct", "speed_avg_kmh", "distance_km", "energy_kwh", "charge_kwh_grid", "temp_max_c", "imbalance_max_mv", "isolation_min_kohm")
    VIN_FIELD_NUMBER: _ClassVar[int]
    MINUTE_START_MS_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    SOC_MIN_PCT_FIELD_NUMBER: _ClassVar[int]
    SOC_MAX_PCT_FIELD_NUMBER: _ClassVar[int]
    SOC_AVG_PCT_FIELD_NUMBER: _ClassVar[int]
    SPEED_AVG_KMH_FIELD_NUMBER: _ClassVar[int]
    DISTANCE_KM_FIELD_NUMBER: _ClassVar[int]
    ENERGY_KWH_FIELD_NUMBER: _ClassVar[int]
    CHARGE_KWH_GRID_FIELD_NUMBER: _ClassVar[int]
    TEMP_MAX_C_FIELD_NUMBER: _ClassVar[int]
    IMBALANCE_MAX_MV_FIELD_NUMBER: _ClassVar[int]
    ISOLATION_MIN_KOHM_FIELD_NUMBER: _ClassVar[int]
    vin: str
    minute_start_ms: int
    count: int
    soc_min_pct: float
    soc_max_pct: float
    soc_avg_pct: float
    speed_avg_kmh: float
    distance_km: float
    energy_kwh: float
    charge_kwh_grid: float
    temp_max_c: float
    imbalance_max_mv: float
    isolation_min_kohm: float
    def __init__(self, vin: _Optional[str] = ..., minute_start_ms: _Optional[int] = ..., count: _Optional[int] = ..., soc_min_pct: _Optional[float] = ..., soc_max_pct: _Optional[float] = ..., soc_avg_pct: _Optional[float] = ..., speed_avg_kmh: _Optional[float] = ..., distance_km: _Optional[float] = ..., energy_kwh: _Optional[float] = ..., charge_kwh_grid: _Optional[float] = ..., temp_max_c: _Optional[float] = ..., imbalance_max_mv: _Optional[float] = ..., isolation_min_kohm: _Optional[float] = ...) -> None: ...

class VehicleState(_message.Message):
    __slots__ = ("vin", "ts_event_ms", "lat", "lon", "speed_kmh", "soc_pct", "pack_temp_max_c", "charge_state", "firing", "rules")
    VIN_FIELD_NUMBER: _ClassVar[int]
    TS_EVENT_MS_FIELD_NUMBER: _ClassVar[int]
    LAT_FIELD_NUMBER: _ClassVar[int]
    LON_FIELD_NUMBER: _ClassVar[int]
    SPEED_KMH_FIELD_NUMBER: _ClassVar[int]
    SOC_PCT_FIELD_NUMBER: _ClassVar[int]
    PACK_TEMP_MAX_C_FIELD_NUMBER: _ClassVar[int]
    CHARGE_STATE_FIELD_NUMBER: _ClassVar[int]
    FIRING_FIELD_NUMBER: _ClassVar[int]
    RULES_FIELD_NUMBER: _ClassVar[int]
    vin: str
    ts_event_ms: int
    lat: float
    lon: float
    speed_kmh: float
    soc_pct: float
    pack_temp_max_c: float
    charge_state: _telemetry_pb2.ChargeState
    firing: _containers.RepeatedScalarFieldContainer[str]
    rules: RuleSnapshot
    def __init__(self, vin: _Optional[str] = ..., ts_event_ms: _Optional[int] = ..., lat: _Optional[float] = ..., lon: _Optional[float] = ..., speed_kmh: _Optional[float] = ..., soc_pct: _Optional[float] = ..., pack_temp_max_c: _Optional[float] = ..., charge_state: _Optional[_Union[_telemetry_pb2.ChargeState, str]] = ..., firing: _Optional[_Iterable[str]] = ..., rules: _Optional[_Union[RuleSnapshot, _Mapping]] = ...) -> None: ...

class RuleSnapshot(_message.Message):
    __slots__ = ("last_ts_ms", "ign_on", "delta_mean", "delta_var", "delta_n", "active_dtc", "machines")
    LAST_TS_MS_FIELD_NUMBER: _ClassVar[int]
    IGN_ON_FIELD_NUMBER: _ClassVar[int]
    DELTA_MEAN_FIELD_NUMBER: _ClassVar[int]
    DELTA_VAR_FIELD_NUMBER: _ClassVar[int]
    DELTA_N_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_DTC_FIELD_NUMBER: _ClassVar[int]
    MACHINES_FIELD_NUMBER: _ClassVar[int]
    last_ts_ms: int
    ign_on: bool
    delta_mean: float
    delta_var: float
    delta_n: int
    active_dtc: _containers.RepeatedScalarFieldContainer[str]
    machines: _containers.RepeatedCompositeFieldContainer[MachineState]
    def __init__(self, last_ts_ms: _Optional[int] = ..., ign_on: bool = ..., delta_mean: _Optional[float] = ..., delta_var: _Optional[float] = ..., delta_n: _Optional[int] = ..., active_dtc: _Optional[_Iterable[str]] = ..., machines: _Optional[_Iterable[_Union[MachineState, _Mapping]]] = ...) -> None: ...

class MachineState(_message.Message):
    __slots__ = ("key", "phase", "start_ms", "clear_ms", "last_ms")
    KEY_FIELD_NUMBER: _ClassVar[int]
    PHASE_FIELD_NUMBER: _ClassVar[int]
    START_MS_FIELD_NUMBER: _ClassVar[int]
    CLEAR_MS_FIELD_NUMBER: _ClassVar[int]
    LAST_MS_FIELD_NUMBER: _ClassVar[int]
    key: str
    phase: int
    start_ms: int
    clear_ms: int
    last_ms: int
    def __init__(self, key: _Optional[str] = ..., phase: _Optional[int] = ..., start_ms: _Optional[int] = ..., clear_ms: _Optional[int] = ..., last_ms: _Optional[int] = ...) -> None: ...
