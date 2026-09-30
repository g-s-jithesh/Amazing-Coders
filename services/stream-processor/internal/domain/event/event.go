// Package event holds the stream-processor's view of a canonical telemetry event and of a vehicle's
// registry entry. Pure (adapters map protobuf ↔ these types).
package event

// Presence bits for optional sensor groups (a dropped-out group is absent, not zero).
const (
	HasGPS uint8 = 1 << iota
	HasTemp
	HasCells
)

// Proto enum numbers (libs/proto telemetry.v1).
const (
	ChargeIdle = 1

	EvtIgnOn   = 2
	EvtIgnOff  = 3
	EvtPlugIn  = 4
	EvtPlugOut = 5
)

type Event struct {
	VIN                        string
	TsEventMs, TsIngestMs      int64
	Seq                        uint64
	Lat, Lon                   float64
	SpeedKmh                   float64
	OdoKm                      float64
	SoCPct                     float64
	PackVoltageV, PackCurrentA float64
	PackTempMinC, PackTempMaxC float64
	CellVMinMv, CellVMaxMv     float64
	IsolationKohm              float64
	HVInterlockOK              bool
	Aux12vV, AmbientC          float64
	ChargeState                int32
	ChargePowerKW              float64
	DTC                        []string
	Evt                        int32
	Has                        uint8
}

func (e *Event) Imbalance() float64 { return e.CellVMaxMv - e.CellVMinMv }

// Vehicle is a registry entry (fleet.vehicle.v1): who owns the VIN and what its duty looks like.
type Vehicle struct {
	VIN, TenantID, FleetID, DepotID string
	DepotLat, DepotLon              float64
	CapacityKWh, WhPerKm, NominalV  float64
	DepartMinIST, ReturnMinIST      int
}
