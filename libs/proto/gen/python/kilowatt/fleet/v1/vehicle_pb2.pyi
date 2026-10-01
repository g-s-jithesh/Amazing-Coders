from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class Vehicle(_message.Message):
    __slots__ = ("vin", "tenant_id", "fleet_id", "depot_id", "depot_lat", "depot_lon", "model_code", "capacity_kwh", "wh_per_km", "depart_min_ist", "return_min_ist", "nominal_voltage_v", "current_pack_id")
    VIN_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    FLEET_ID_FIELD_NUMBER: _ClassVar[int]
    DEPOT_ID_FIELD_NUMBER: _ClassVar[int]
    DEPOT_LAT_FIELD_NUMBER: _ClassVar[int]
    DEPOT_LON_FIELD_NUMBER: _ClassVar[int]
    MODEL_CODE_FIELD_NUMBER: _ClassVar[int]
    CAPACITY_KWH_FIELD_NUMBER: _ClassVar[int]
    WH_PER_KM_FIELD_NUMBER: _ClassVar[int]
    DEPART_MIN_IST_FIELD_NUMBER: _ClassVar[int]
    RETURN_MIN_IST_FIELD_NUMBER: _ClassVar[int]
    NOMINAL_VOLTAGE_V_FIELD_NUMBER: _ClassVar[int]
    CURRENT_PACK_ID_FIELD_NUMBER: _ClassVar[int]
    vin: str
    tenant_id: str
    fleet_id: str
    depot_id: str
    depot_lat: float
    depot_lon: float
    model_code: str
    capacity_kwh: float
    wh_per_km: float
    depart_min_ist: int
    return_min_ist: int
    nominal_voltage_v: float
    current_pack_id: str
    def __init__(self, vin: _Optional[str] = ..., tenant_id: _Optional[str] = ..., fleet_id: _Optional[str] = ..., depot_id: _Optional[str] = ..., depot_lat: _Optional[float] = ..., depot_lon: _Optional[float] = ..., model_code: _Optional[str] = ..., capacity_kwh: _Optional[float] = ..., wh_per_km: _Optional[float] = ..., depart_min_ist: _Optional[int] = ..., return_min_ist: _Optional[int] = ..., nominal_voltage_v: _Optional[float] = ..., current_pack_id: _Optional[str] = ...) -> None: ...
