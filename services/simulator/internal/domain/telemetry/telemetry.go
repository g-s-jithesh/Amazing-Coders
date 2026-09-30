// Package telemetry is the simulator's in-memory canonical sample. Field meanings and units follow
// libs/proto/kilowatt/telemetry/v1/telemetry.proto; OEM encoders translate it to wire formats.
// event_id and ts_ingest_ms are assigned by the gateway, so they are absent here.
package telemetry

// ChargeState and EventType values equal the proto enum numbers.
type ChargeState int32

const (
	ChargeIdle   ChargeState = 1
	ChargeAC     ChargeState = 2
	ChargeDCFast ChargeState = 3
	ChargeFault  ChargeState = 4
)

type EventType int32

const (
	EvtPeriodic   EventType = 1
	EvtIgnOn      EventType = 2
	EvtIgnOff     EventType = 3
	EvtPlugIn     EventType = 4
	EvtPlugOut    EventType = 5
	EvtHarshBrake EventType = 6
	EvtHarshAccel EventType = 7
	EvtDTCRaised  EventType = 8
)

// SchemaVersion is the canonical schema version the simulator emits.
const SchemaVersion = 1

// Malformed marks a sample the noise layer deliberately corrupted. Each kind maps to exactly one
// ingest-gateway DLQ reason, so DLQ counts can be checked against what was injected.
type Malformed uint8

const (
	WellFormed       Malformed = iota
	MalVINChecksum             // → VIN_CHECKSUM
	MalDTCFormat               // → DTC_FORMAT
	MalSchemaVersion           // → UNKNOWN_SCHEMA_VERSION
	MalRange                   // → RANGE
	MalDecode                  // → DECODE (encoder truncates the payload)
)

// Dropout bits mark sensor groups missing from a sample; encoders omit those fields.
const (
	DropGPS   uint8 = 1 << iota // lat, lon, speed
	DropTemp                    // pack temps
	DropCells                   // cell voltages
)

type Sample struct {
	VIN, OEM                   string
	TsEventMs                  int64
	Seq                        uint64
	Lat, Lon                   float64
	SpeedKmh                   float32
	OdoKm                      float64
	SoCPct                     float32
	PackVoltageV, PackCurrentA float32
	PackTempMinC, PackTempMaxC float32
	CellVMinMv, CellVMaxMv     uint32
	IsolationKohm              float32
	HVInterlockOK              bool
	Aux12vV, AmbientC          float32
	ChargeState                ChargeState
	ChargePowerKW              float32
	DTC                        []string
	Evt                        EventType
	SchemaVersion              uint32
	Dropout                    uint8     // DropGPS | DropTemp | DropCells
	Malformed                  Malformed // set only by the noise layer
}
