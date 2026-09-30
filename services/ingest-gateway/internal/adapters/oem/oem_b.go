package oem

import (
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// B decodes oem_b: JSON arrays (HTTPS batches), °F, miles, epoch seconds, nested battery{}, SoC 0–1.
type B struct{}

const kmPerMile = 1.609344

func fToC(f float32) float32 { return (f - 32) * 5 / 9 }

type bRec struct {
	VehicleID *string `json:"vehicleId"`
	Version   *uint32 `json:"version"`
	Epoch     *int64  `json:"epoch"`
	Sequence  *uint64 `json:"sequence"`
	EventType *string `json:"eventType"`
	Location  *struct {
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
		SpeedMph  *float64 `json:"speedMph"`
	} `json:"location"`
	OdometerMiles *float64 `json:"odometerMiles"`
	Battery       *struct {
		SoC           *float64 `json:"soc"`
		Voltage       *float32 `json:"voltage"`
		Current       *float32 `json:"current"`
		TempMinF      *float32 `json:"tempMinF"`
		TempMaxF      *float32 `json:"tempMaxF"`
		CellMinMv     *uint32  `json:"cellMinMv"`
		CellMaxMv     *uint32  `json:"cellMaxMv"`
		IsolationKohm *float32 `json:"isolationKohm"`
	} `json:"battery"`
	HVInterlock *bool    `json:"hvInterlock"`
	AuxVolts    *float32 `json:"auxVolts"`
	AmbientF    *float32 `json:"ambientF"`
	Charging    *struct {
		Status  *string  `json:"status"`
		PowerKW *float32 `json:"powerKw"`
	} `json:"charging"`
	DTCs []string `json:"dtcs"`
}

// Decode accepts a batch array; a single object (batch=false) is decoded as a one-record batch.
func (B) Decode(body []byte, batch bool) ([]canonical.Record, *canonical.Reject) {
	raws, rej := jsonRecords(body, batch)
	if rej != nil {
		return nil, rej
	}
	out := make([]canonical.Record, len(raws))
	for i, raw := range raws {
		out[i] = decodeB(raw)
	}
	return out, nil
}

func decodeB(raw []byte) canonical.Record {
	if r := probeVersion(raw, "version"); r != nil {
		return canonical.Record{Raw: raw, Reject: r}
	}
	var m bRec
	if r := strictUnmarshal(raw, &m); r != nil {
		return canonical.Record{Raw: raw, Reject: r}
	}
	if m.Battery == nil || m.Charging == nil {
		return reject(raw, canonical.Schema, "missing battery or charging object")
	}
	bt := m.Battery
	if f := missing(map[string]bool{
		"vehicleId": m.VehicleID != nil, "epoch": m.Epoch != nil, "sequence": m.Sequence != nil, "eventType": m.EventType != nil,
		"odometerMiles": m.OdometerMiles != nil, "battery.soc": bt.SoC != nil, "battery.voltage": bt.Voltage != nil,
		"battery.current": bt.Current != nil, "battery.isolationKohm": bt.IsolationKohm != nil, "hvInterlock": m.HVInterlock != nil,
		"auxVolts": m.AuxVolts != nil, "ambientF": m.AmbientF != nil, "charging.status": m.Charging.Status != nil,
		"charging.powerKw": m.Charging.PowerKW != nil,
	}); f != "" {
		return reject(raw, canonical.Schema, "missing %s", f)
	}
	evt, ok := evtByName[*m.EventType]
	if !ok {
		return reject(raw, canonical.Schema, "unknown eventType %q", *m.EventType)
	}
	cs, ok := chargeByName[*m.Charging.Status]
	if !ok {
		return reject(raw, canonical.Schema, "unknown charging.status %q", *m.Charging.Status)
	}
	e := canonical.Event{
		VIN: *m.VehicleID, OEM: "oem_b", TsEventMs: *m.Epoch * 1000, Seq: *m.Sequence,
		OdoKm: *m.OdometerMiles * kmPerMile, SoCPct: float32(*bt.SoC * 100),
		PackVoltageV: *bt.Voltage, PackCurrentA: *bt.Current, IsolationKohm: *bt.IsolationKohm,
		HVInterlockOK: *m.HVInterlock, Aux12vV: *m.AuxVolts, AmbientC: fToC(*m.AmbientF),
		ChargeState: cs, ChargePowerKW: *m.Charging.PowerKW, DTC: m.DTCs, Evt: evt, SchemaVersion: *m.Version,
	}
	if l := m.Location; l != nil {
		if l.Latitude == nil || l.Longitude == nil || l.SpeedMph == nil {
			return reject(raw, canonical.Schema, "partial location group")
		}
		e.Lat, e.Lon, e.SpeedKmh, e.Has = *l.Latitude, *l.Longitude, float32(*l.SpeedMph*kmPerMile), e.Has|canonical.HasGPS
	}
	if bt.TempMinF != nil || bt.TempMaxF != nil {
		if bt.TempMinF == nil || bt.TempMaxF == nil {
			return reject(raw, canonical.Schema, "partial temperature group")
		}
		e.PackTempMinC, e.PackTempMaxC, e.Has = fToC(*bt.TempMinF), fToC(*bt.TempMaxF), e.Has|canonical.HasTemp
	}
	if bt.CellMinMv != nil || bt.CellMaxMv != nil {
		if bt.CellMinMv == nil || bt.CellMaxMv == nil {
			return reject(raw, canonical.Schema, "partial cell group")
		}
		e.CellVMinMv, e.CellVMaxMv, e.Has = *bt.CellMinMv, *bt.CellMaxMv, e.Has|canonical.HasCells
	}
	return canonical.Record{Raw: raw, Event: e}
}
