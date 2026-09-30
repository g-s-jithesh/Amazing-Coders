// Package encoders turns canonical samples into the three simulated OEM wire formats (CLAUDE.md §4.1).
// The ingest-gateway's adapters are the inverse; libs/oem-samples holds the shared golden files.
//
//	oem_a  MQTT  JSON, metric units, ISO-8601 UTC timestamps
//	oem_b  HTTPS JSON array batches, °F, miles, epoch *seconds*, nested battery{}, SoC 0–1
//	oem_c  MQTT  compact positional text (";"), DTCs as SAE J2012 2-byte hex
//
// Dropout bits omit fields; MalDecode truncates the payload so it cannot be parsed.
package encoders

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/dtc"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

// Encoder appends one encoded sample to dst.
type Encoder func(s *telemetry.Sample, dst []byte) ([]byte, error)

// ByOEM is the encoder registry.
var ByOEM = map[string]Encoder{"oem_a": EncodeA, "oem_b": EncodeB, "oem_c": EncodeC}

func truncateIfDecode(s *telemetry.Sample, start int, dst []byte) []byte {
	if s.Malformed == telemetry.MalDecode {
		return dst[:start+(len(dst)-start)/2]
	}
	return dst
}

func f32(x float32) *float32 { return &x }
func u32(x uint32) *uint32   { return &x }

// ---- oem_a ----

type aPos struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	SpeedKmh float32 `json:"speed_kmh"`
}

type aPack struct {
	VoltageV      float32  `json:"voltage_v"`
	CurrentA      float32  `json:"current_a"`
	TempMinC      *float32 `json:"temp_min_c,omitempty"`
	TempMaxC      *float32 `json:"temp_max_c,omitempty"`
	CellMinMv     *uint32  `json:"cell_min_mv,omitempty"`
	CellMaxMv     *uint32  `json:"cell_max_mv,omitempty"`
	IsolationKohm float32  `json:"isolation_kohm"`
	HVInterlockOK bool     `json:"hv_interlock_ok"`
}

type aCharge struct {
	State   string  `json:"state"`
	PowerKW float32 `json:"power_kw"`
}

type aMsg struct {
	VIN      string   `json:"vin"`
	Schema   uint32   `json:"schema"`
	Ts       string   `json:"ts"`
	Seq      uint64   `json:"seq"`
	Evt      string   `json:"evt"`
	Pos      *aPos    `json:"pos,omitempty"`
	OdoKm    float64  `json:"odo_km"`
	SoCPct   float32  `json:"soc_pct"`
	Pack     aPack    `json:"pack"`
	Aux12vV  float32  `json:"aux_12v_v"`
	AmbientC float32  `json:"ambient_c"`
	Charge   aCharge  `json:"charge"`
	DTC      []string `json:"dtc"`
}

// ponytail: encoding/json allocates ~1 µs/event; switch to an append-based writer if profiling shows it matters.
func EncodeA(s *telemetry.Sample, dst []byte) ([]byte, error) {
	m := aMsg{
		VIN: s.VIN, Schema: s.SchemaVersion, Seq: s.Seq, Evt: s.Evt.String(),
		Ts:    time.UnixMilli(s.TsEventMs).UTC().Format("2006-01-02T15:04:05.000Z"),
		OdoKm: s.OdoKm, SoCPct: s.SoCPct, Aux12vV: s.Aux12vV, AmbientC: s.AmbientC,
		Pack:   aPack{VoltageV: s.PackVoltageV, CurrentA: s.PackCurrentA, IsolationKohm: s.IsolationKohm, HVInterlockOK: s.HVInterlockOK},
		Charge: aCharge{s.ChargeState.String(), s.ChargePowerKW},
		DTC:    s.DTC,
	}
	if m.DTC == nil {
		m.DTC = []string{}
	}
	if s.Dropout&telemetry.DropGPS == 0 {
		m.Pos = &aPos{s.Lat, s.Lon, s.SpeedKmh}
	}
	if s.Dropout&telemetry.DropTemp == 0 {
		m.Pack.TempMinC, m.Pack.TempMaxC = f32(s.PackTempMinC), f32(s.PackTempMaxC)
	}
	if s.Dropout&telemetry.DropCells == 0 {
		m.Pack.CellMinMv, m.Pack.CellMaxMv = u32(s.CellVMinMv), u32(s.CellVMaxMv)
	}
	return marshalAppend(s, m, dst)
}

func marshalAppend(s *telemetry.Sample, v any, dst []byte) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return dst, err
	}
	start := len(dst)
	return truncateIfDecode(s, start, append(dst, b...)), nil
}

// ---- oem_b ----

const (
	kmPerMile = 1.609344
)

func toF(c float32) float32 { return c*9/5 + 32 }

type bLoc struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	SpeedMph  float64 `json:"speedMph"`
}

type bBattery struct {
	SoC           float64  `json:"soc"` // 0–1
	Voltage       float32  `json:"voltage"`
	Current       float32  `json:"current"`
	TempMinF      *float32 `json:"tempMinF,omitempty"`
	TempMaxF      *float32 `json:"tempMaxF,omitempty"`
	CellMinMv     *uint32  `json:"cellMinMv,omitempty"`
	CellMaxMv     *uint32  `json:"cellMaxMv,omitempty"`
	IsolationKohm float32  `json:"isolationKohm"`
}

type bCharging struct {
	Status  string  `json:"status"`
	PowerKW float32 `json:"powerKw"`
}

type bRec struct {
	VehicleID     string    `json:"vehicleId"`
	Version       uint32    `json:"version"`
	Epoch         int64     `json:"epoch"` // seconds: the ms part is lost (a real quirk; seq keeps order)
	Sequence      uint64    `json:"sequence"`
	EventType     string    `json:"eventType"`
	Location      *bLoc     `json:"location,omitempty"`
	OdometerMiles float64   `json:"odometerMiles"`
	Battery       bBattery  `json:"battery"`
	HVInterlock   bool      `json:"hvInterlock"`
	AuxVolts      float32   `json:"auxVolts"`
	AmbientF      float32   `json:"ambientF"`
	Charging      bCharging `json:"charging"`
	DTCs          []string  `json:"dtcs"`
}

// EncodeB encodes ONE record (a JSON object). Batches are "[" + records joined by "," + "]";
// see JoinBatch.
func EncodeB(s *telemetry.Sample, dst []byte) ([]byte, error) {
	r := bRec{
		VehicleID: s.VIN, Version: s.SchemaVersion, Epoch: s.TsEventMs / 1000, Sequence: s.Seq, EventType: s.Evt.String(),
		OdometerMiles: s.OdoKm / kmPerMile,
		Battery:       bBattery{SoC: float64(s.SoCPct) / 100, Voltage: s.PackVoltageV, Current: s.PackCurrentA, IsolationKohm: s.IsolationKohm},
		HVInterlock:   s.HVInterlockOK, AuxVolts: s.Aux12vV, AmbientF: toF(s.AmbientC),
		Charging: bCharging{s.ChargeState.String(), s.ChargePowerKW},
		DTCs:     s.DTC,
	}
	if r.DTCs == nil {
		r.DTCs = []string{}
	}
	if s.Dropout&telemetry.DropGPS == 0 {
		r.Location = &bLoc{s.Lat, s.Lon, float64(s.SpeedKmh) / kmPerMile}
	}
	if s.Dropout&telemetry.DropTemp == 0 {
		r.Battery.TempMinF, r.Battery.TempMaxF = f32(toF(s.PackTempMinC)), f32(toF(s.PackTempMaxC))
	}
	if s.Dropout&telemetry.DropCells == 0 {
		r.Battery.CellMinMv, r.Battery.CellMaxMv = u32(s.CellVMinMv), u32(s.CellVMaxMv)
	}
	return marshalAppend(s, r, dst)
}

// JoinBatch wraps already-encoded oem_b records into one JSON array body.
func JoinBatch(records [][]byte, dst []byte) []byte {
	dst = append(dst, '[')
	for i, r := range records {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, r...)
	}
	return append(dst, ']')
}

// ---- oem_c ----

// CFields documents the oem_c positional layout (v1).
const CFields = "schema;vin;ts_ms;seq;evt;lat;lon;speed_kmh;odo_km;soc_pct;pack_v;pack_a;t_min_c;t_max_c;cell_min_mv;cell_max_mv;iso_kohm;hvil;aux_v;amb_c;charge_state;charge_kw;dtc_hex"

func appendF(dst []byte, x float64, prec int) []byte {
	return strconv.AppendFloat(dst, x, 'f', prec, 64)
}

func EncodeC(s *telemetry.Sample, dst []byte) ([]byte, error) {
	start := len(dst)
	sep := func() { dst = append(dst, ';') }
	dst = strconv.AppendUint(dst, uint64(s.SchemaVersion), 10)
	sep()
	dst = append(dst, s.VIN...)
	sep()
	dst = strconv.AppendInt(dst, s.TsEventMs, 10)
	sep()
	dst = strconv.AppendUint(dst, s.Seq, 10)
	sep()
	dst = strconv.AppendInt(dst, int64(s.Evt), 10)
	sep()
	if s.Dropout&telemetry.DropGPS == 0 {
		dst = appendF(dst, s.Lat, 6)
		sep()
		dst = appendF(dst, s.Lon, 6)
		sep()
		dst = appendF(dst, float64(s.SpeedKmh), 1)
	} else {
		dst = append(dst, ";;"...)
	}
	sep()
	dst = appendF(dst, s.OdoKm, 3)
	sep()
	dst = appendF(dst, float64(s.SoCPct), 2)
	sep()
	dst = appendF(dst, float64(s.PackVoltageV), 1)
	sep()
	dst = appendF(dst, float64(s.PackCurrentA), 1)
	sep()
	if s.Dropout&telemetry.DropTemp == 0 {
		dst = appendF(dst, float64(s.PackTempMinC), 1)
		sep()
		dst = appendF(dst, float64(s.PackTempMaxC), 1)
	} else {
		sep()
	}
	sep()
	if s.Dropout&telemetry.DropCells == 0 {
		dst = strconv.AppendUint(dst, uint64(s.CellVMinMv), 10)
		sep()
		dst = strconv.AppendUint(dst, uint64(s.CellVMaxMv), 10)
	} else {
		sep()
	}
	sep()
	dst = appendF(dst, float64(s.IsolationKohm), 0)
	sep()
	if s.HVInterlockOK {
		dst = append(dst, '1')
	} else {
		dst = append(dst, '0')
	}
	sep()
	dst = appendF(dst, float64(s.Aux12vV), 2)
	sep()
	dst = appendF(dst, float64(s.AmbientC), 1)
	sep()
	dst = strconv.AppendInt(dst, int64(s.ChargeState), 10)
	sep()
	dst = appendF(dst, float64(s.ChargePowerKW), 2)
	sep()
	for _, code := range s.DTC {
		v, err := dtc.Encode(code)
		if err != nil {
			dst = append(dst, "ZZZZ"...) // unencodable code: gateway must reject as DTC_FORMAT
			continue
		}
		dst = fmt.Appendf(dst, "%04X", v)
	}
	return truncateIfDecode(s, start, dst), nil
}

// ---- golden samples (libs/oem-samples) ----

// GoldenVIN is a valid synthetic VIN (0KC… = oem_c WMI, DV45N, 2024, Chennai, serial 1).
const GoldenVIN = "0KCDV45N9RC000001"

// GoldenSample is the fixed canonical sample behind every golden file.
func GoldenSample() telemetry.Sample {
	return telemetry.Sample{
		VIN: GoldenVIN, OEM: "oem_c", TsEventMs: 1788238800123, Seq: 4242,
		Lat: 13.082700, Lon: 80.270700, SpeedKmh: 42.5, OdoKm: 18234.125,
		SoCPct: 63.25, PackVoltageV: 362.4, PackCurrentA: 21.7, PackTempMinC: 30.4, PackTempMaxC: 33.1,
		CellVMinMv: 3765, CellVMaxMv: 3779, IsolationKohm: 2480, HVInterlockOK: true,
		Aux12vV: 13.9, AmbientC: 31.5, ChargeState: telemetry.ChargeIdle, ChargePowerKW: 0,
		DTC: []string{"P0A7E", "U0111"}, Evt: telemetry.EvtDTCRaised, SchemaVersion: telemetry.SchemaVersion,
	}
}

// GoldenCases are the variants written per OEM: valid, one per dropout group, one per malformed kind.
// Malformed variants are produced exactly as the noise layer produces them.
func GoldenCases() map[string]telemetry.Sample {
	base := GoldenSample()
	with := func(f func(*telemetry.Sample)) telemetry.Sample {
		s := GoldenSample()
		f(&s)
		return s
	}
	return map[string]telemetry.Sample{
		"valid":         base,
		"dropout_gps":   with(func(s *telemetry.Sample) { s.Dropout = telemetry.DropGPS }),
		"dropout_temp":  with(func(s *telemetry.Sample) { s.Dropout = telemetry.DropTemp }),
		"dropout_cells": with(func(s *telemetry.Sample) { s.Dropout = telemetry.DropCells }),
		"malformed_vin_checksum": with(func(s *telemetry.Sample) {
			s.Malformed, s.VIN = telemetry.MalVINChecksum, GoldenVIN[:8]+"7"+GoldenVIN[9:]
		}),
		"malformed_dtc_format":     with(func(s *telemetry.Sample) { s.Malformed, s.DTC = telemetry.MalDTCFormat, []string{"PX12Z"} }),
		"malformed_schema_version": with(func(s *telemetry.Sample) { s.Malformed, s.SchemaVersion = telemetry.MalSchemaVersion, 99 }),
		"malformed_range":          with(func(s *telemetry.Sample) { s.Malformed, s.SoCPct = telemetry.MalRange, 250 }),
		"malformed_decode":         with(func(s *telemetry.Sample) { s.Malformed = telemetry.MalDecode }),
	}
}

// Ext is the golden file extension per OEM.
var Ext = map[string]string{"oem_a": ".json", "oem_b": ".json", "oem_c": ".txt"}

// Golden renders every case for one OEM. oem_b files hold a one-record batch (its wire format).
func Golden(oem string) (map[string][]byte, error) {
	enc, ok := ByOEM[oem]
	if !ok {
		return nil, fmt.Errorf("encoders: unknown oem %q", oem)
	}
	out := map[string][]byte{}
	for name, s := range GoldenCases() {
		s.OEM = oem
		b, err := enc(&s, nil)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", oem, name, err)
		}
		if oem == "oem_b" {
			b = JoinBatch([][]byte{b}, nil)
		}
		out[name+Ext[oem]] = b
	}
	return out, nil
}
