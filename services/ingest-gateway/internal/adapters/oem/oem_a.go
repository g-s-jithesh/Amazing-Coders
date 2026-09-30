package oem

import (
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// A decodes oem_a: JSON objects, metric units, ISO-8601 UTC timestamps.
type A struct{}

type aMsg struct {
	VIN    *string `json:"vin"`
	Schema *uint32 `json:"schema"`
	Ts     *string `json:"ts"`
	Seq    *uint64 `json:"seq"`
	Evt    *string `json:"evt"`
	Pos    *struct {
		Lat      *float64 `json:"lat"`
		Lon      *float64 `json:"lon"`
		SpeedKmh *float32 `json:"speed_kmh"`
	} `json:"pos"`
	OdoKm  *float64 `json:"odo_km"`
	SoCPct *float32 `json:"soc_pct"`
	Pack   *struct {
		VoltageV      *float32 `json:"voltage_v"`
		CurrentA      *float32 `json:"current_a"`
		TempMinC      *float32 `json:"temp_min_c"`
		TempMaxC      *float32 `json:"temp_max_c"`
		CellMinMv     *uint32  `json:"cell_min_mv"`
		CellMaxMv     *uint32  `json:"cell_max_mv"`
		IsolationKohm *float32 `json:"isolation_kohm"`
		HVInterlockOK *bool    `json:"hv_interlock_ok"`
	} `json:"pack"`
	Aux12vV  *float32 `json:"aux_12v_v"`
	AmbientC *float32 `json:"ambient_c"`
	Charge   *struct {
		State   *string  `json:"state"`
		PowerKW *float32 `json:"power_kw"`
	} `json:"charge"`
	DTC []string `json:"dtc"`
}

func (A) Decode(body []byte, batch bool) ([]canonical.Record, *canonical.Reject) {
	raws, rej := jsonRecords(body, batch)
	if rej != nil {
		return nil, rej
	}
	out := make([]canonical.Record, len(raws))
	for i, raw := range raws {
		out[i] = decodeA(raw)
	}
	return out, nil
}

func decodeA(raw []byte) canonical.Record {
	if r := probeVersion(raw, "schema"); r != nil {
		return canonical.Record{Raw: raw, Reject: r}
	}
	var m aMsg
	if r := strictUnmarshal(raw, &m); r != nil {
		return canonical.Record{Raw: raw, Reject: r}
	}
	if m.Pack == nil || m.Charge == nil {
		return reject(raw, canonical.Schema, "missing pack or charge object")
	}
	if f := missing(map[string]bool{
		"vin": m.VIN != nil, "ts": m.Ts != nil, "seq": m.Seq != nil, "evt": m.Evt != nil, "odo_km": m.OdoKm != nil,
		"soc_pct": m.SoCPct != nil, "pack.voltage_v": m.Pack.VoltageV != nil, "pack.current_a": m.Pack.CurrentA != nil,
		"pack.isolation_kohm": m.Pack.IsolationKohm != nil, "pack.hv_interlock_ok": m.Pack.HVInterlockOK != nil,
		"aux_12v_v": m.Aux12vV != nil, "ambient_c": m.AmbientC != nil, "charge.state": m.Charge.State != nil,
		"charge.power_kw": m.Charge.PowerKW != nil,
	}); f != "" {
		return reject(raw, canonical.Schema, "missing %s", f)
	}
	ts, err := time.Parse(time.RFC3339Nano, *m.Ts)
	if err != nil {
		return reject(raw, canonical.Schema, "ts %q is not ISO-8601", *m.Ts)
	}
	evt, ok := evtByName[*m.Evt]
	if !ok {
		return reject(raw, canonical.Schema, "unknown evt %q", *m.Evt)
	}
	cs, ok := chargeByName[*m.Charge.State]
	if !ok {
		return reject(raw, canonical.Schema, "unknown charge.state %q", *m.Charge.State)
	}
	e := canonical.Event{
		VIN: *m.VIN, OEM: "oem_a", TsEventMs: ts.UnixMilli(), Seq: *m.Seq, OdoKm: *m.OdoKm, SoCPct: *m.SoCPct,
		PackVoltageV: *m.Pack.VoltageV, PackCurrentA: *m.Pack.CurrentA, IsolationKohm: *m.Pack.IsolationKohm,
		HVInterlockOK: *m.Pack.HVInterlockOK, Aux12vV: *m.Aux12vV, AmbientC: *m.AmbientC,
		ChargeState: cs, ChargePowerKW: *m.Charge.PowerKW, DTC: m.DTC, Evt: evt, SchemaVersion: *m.Schema,
	}
	if p := m.Pos; p != nil {
		if p.Lat == nil || p.Lon == nil || p.SpeedKmh == nil {
			return reject(raw, canonical.Schema, "partial pos group")
		}
		e.Lat, e.Lon, e.SpeedKmh, e.Has = *p.Lat, *p.Lon, *p.SpeedKmh, e.Has|canonical.HasGPS
	}
	if pk := m.Pack; pk.TempMinC != nil || pk.TempMaxC != nil {
		if pk.TempMinC == nil || pk.TempMaxC == nil {
			return reject(raw, canonical.Schema, "partial temperature group")
		}
		e.PackTempMinC, e.PackTempMaxC, e.Has = *pk.TempMinC, *pk.TempMaxC, e.Has|canonical.HasTemp
	}
	if pk := m.Pack; pk.CellMinMv != nil || pk.CellMaxMv != nil {
		if pk.CellMinMv == nil || pk.CellMaxMv == nil {
			return reject(raw, canonical.Schema, "partial cell group")
		}
		e.CellVMinMv, e.CellVMaxMv, e.Has = *pk.CellMinMv, *pk.CellMaxMv, e.Has|canonical.HasCells
	}
	return canonical.Record{Raw: raw, Event: e}
}
