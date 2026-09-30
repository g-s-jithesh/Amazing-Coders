// Package canonical is the gateway's in-memory canonical telemetry event (mirrors
// libs/proto/kilowatt/telemetry/v1/telemetry.proto) plus the rejection reasons and range rules.
// Pure: no framework, broker or protobuf imports.
package canonical

import (
	"fmt"
	"math"
)

// SupportedSchema is the only canonical schema version this gateway accepts.
const SupportedSchema = 1

// Presence bits for the optional sensor groups (a dropped-out group is absent, not zero).
const (
	HasGPS uint8 = 1 << iota
	HasTemp
	HasCells
	HasAll = HasGPS | HasTemp | HasCells
)

type Event struct {
	EventID                    string
	VIN, OEM                   string
	TsEventMs, TsIngestMs      int64
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
	ChargeState                int32 // proto enum number
	ChargePowerKW              float32
	DTC                        []string
	Evt                        int32 // proto enum number
	SchemaVersion              uint32
	Has                        uint8
}

// Reason is a DLQ reason (services/ingest-gateway/CLAUDE.md). The set is closed.
type Reason string

const (
	Auth                 Reason = "AUTH"
	IdentityMismatch     Reason = "IDENTITY_MISMATCH"
	Oversize             Reason = "OVERSIZE"
	UnknownOEM           Reason = "UNKNOWN_OEM"
	UnknownSchemaVersion Reason = "UNKNOWN_SCHEMA_VERSION"
	Decode               Reason = "DECODE"
	Schema               Reason = "SCHEMA"
	VINFormat            Reason = "VIN_FORMAT"
	VINChecksum          Reason = "VIN_CHECKSUM"
	DTCFormat            Reason = "DTC_FORMAT"
	Range                Reason = "RANGE"
)

// Reasons lists every DLQ reason (for metrics initialisation and tests).
var Reasons = []Reason{Auth, IdentityMismatch, Oversize, UnknownOEM, UnknownSchemaVersion, Decode, Schema, VINFormat, VINChecksum, DTCFormat, Range}

// Reject is why a message or record went to the DLQ.
type Reject struct {
	Reason Reason
	Detail string
}

func (r *Reject) Error() string { return string(r.Reason) + ": " + r.Detail }

func Rejectf(reason Reason, format string, a ...any) *Reject {
	return &Reject{reason, fmt.Sprintf(format, a...)}
}

// in reports lo ≤ x ≤ hi, and false for NaN.
func in(x, lo, hi float64) bool { return x >= lo && x <= hi }

// Plausibility bounds. Anything outside is a sensor/encoding fault, not a vehicle state.
const (
	minTsMs = 1_577_836_800_000 // 2020-01-01T00:00:00Z
	dayMs   = 86_400_000
)

// CheckRange applies physical plausibility bounds. receivedMs bounds the event clock to at most a
// day in the future (vehicle clocks drift; buffered replays arrive late, never far ahead).
func (e *Event) CheckRange(receivedMs int64) *Reject {
	type rule struct {
		name     string
		v        float64
		lo, hi   float64
		required bool
	}
	gps, temp, cells := e.Has&HasGPS != 0, e.Has&HasTemp != 0, e.Has&HasCells != 0
	rules := []rule{
		{"soc_pct", float64(e.SoCPct), 0, 100, true},
		{"pack_voltage_v", float64(e.PackVoltageV), 0, 1000, true},
		{"pack_current_a", float64(e.PackCurrentA), -1000, 1000, true},
		{"odo_km", e.OdoKm, 0, 5_000_000, true},
		{"isolation_kohm", float64(e.IsolationKohm), 0, 100_000, true},
		{"aux_12v_v", float64(e.Aux12vV), 0, 20, true},
		{"ambient_c", float64(e.AmbientC), -40, 60, true},
		{"charge_power_kw", float64(e.ChargePowerKW), 0, 400, true},
		{"lat", e.Lat, -90, 90, gps},
		{"lon", e.Lon, -180, 180, gps},
		{"speed_kmh", float64(e.SpeedKmh), 0, 250, gps},
		{"pack_temp_min_c", float64(e.PackTempMinC), -40, 90, temp},
		{"pack_temp_max_c", float64(e.PackTempMaxC), -40, 90, temp},
		{"cell_v_min_mv", float64(e.CellVMinMv), 1500, 5000, cells},
		{"cell_v_max_mv", float64(e.CellVMaxMv), 1500, 5000, cells},
	}
	for _, r := range rules {
		if r.required && !in(r.v, r.lo, r.hi) {
			return Rejectf(Range, "%s=%v outside [%v, %v]", r.name, r.v, r.lo, r.hi)
		}
	}
	if temp && e.PackTempMinC > e.PackTempMaxC {
		return Rejectf(Range, "pack_temp_min_c %v > max %v", e.PackTempMinC, e.PackTempMaxC)
	}
	if cells && e.CellVMinMv > e.CellVMaxMv {
		return Rejectf(Range, "cell_v_min_mv %d > max %d", e.CellVMinMv, e.CellVMaxMv)
	}
	if e.TsEventMs < minTsMs || e.TsEventMs > receivedMs+dayMs {
		return Rejectf(Range, "ts_event_ms %d implausible (received %d)", e.TsEventMs, receivedMs)
	}
	if e.ChargeState < 1 || e.ChargeState > 4 || e.Evt < 1 || e.Evt > 8 {
		return Rejectf(Range, "enum out of range: charge_state=%d evt=%d", e.ChargeState, e.Evt)
	}
	if math.IsInf(e.OdoKm, 0) {
		return Rejectf(Range, "odo_km infinite")
	}
	return nil
}

// Record is one decoded item of a message: its own raw bytes (what goes to the DLQ if it fails),
// the event, or the reason it was rejected.
type Record struct {
	Raw    []byte
	Event  Event
	Reject *Reject
}
