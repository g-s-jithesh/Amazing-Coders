package canonical

import (
	"math"
	"testing"
)

const recv = 1_788_238_800_000 // 2026-09-01

func good() Event {
	return Event{
		VIN: "0KCDV45N9RC000001", OEM: "oem_c", TsEventMs: recv - 1000, Seq: 1,
		Lat: 13.08, Lon: 80.27, SpeedKmh: 40, OdoKm: 1000, SoCPct: 60,
		PackVoltageV: 360, PackCurrentA: 20, PackTempMinC: 30, PackTempMaxC: 32,
		CellVMinMv: 3700, CellVMaxMv: 3712, IsolationKohm: 2500, HVInterlockOK: true,
		Aux12vV: 13.9, AmbientC: 31, ChargeState: 1, ChargePowerKW: 0, Evt: 1, SchemaVersion: 1, Has: HasAll,
	}
}

func TestCheckRange(t *testing.T) {
	nan := float32(math.NaN())
	cases := map[string]func(*Event){
		"soc high":         func(e *Event) { e.SoCPct = 250 },
		"soc NaN":          func(e *Event) { e.SoCPct = nan },
		"voltage negative": func(e *Event) { e.PackVoltageV = -1 },
		"current huge":     func(e *Event) { e.PackCurrentA = 5000 },
		"temp too hot":     func(e *Event) { e.PackTempMaxC = 120 },
		"temp min > max":   func(e *Event) { e.PackTempMinC = 40 },
		"cell too low":     func(e *Event) { e.CellVMinMv = 100 },
		"cell min > max":   func(e *Event) { e.CellVMinMv = 3800 },
		"lat":              func(e *Event) { e.Lat = 95 },
		"speed":            func(e *Event) { e.SpeedKmh = 400 },
		"ts before 2020":   func(e *Event) { e.TsEventMs = 1_000 },
		"ts far future":    func(e *Event) { e.TsEventMs = recv + 2*dayMs },
		"charge enum":      func(e *Event) { e.ChargeState = 9 },
		"evt enum":         func(e *Event) { e.Evt = 0 },
		"odo infinite":     func(e *Event) { e.OdoKm = math.Inf(1) },
		"charge power":     func(e *Event) { e.ChargePowerKW = -3 },
		"aux":              func(e *Event) { e.Aux12vV = 50 },
		"ambient":          func(e *Event) { e.AmbientC = -60 },
		"isolation":        func(e *Event) { e.IsolationKohm = -1 },
	}
	for name, mutate := range cases {
		e := good()
		mutate(&e)
		if r := e.CheckRange(recv); r == nil || r.Reason != Range {
			t.Errorf("%s: got %v, want RANGE", name, r)
		}
	}
	e := good()
	if r := e.CheckRange(recv); r != nil {
		t.Fatalf("good event rejected: %v", r)
	}
	// Absent groups are not range-checked: a dropped-out GPS with zero lat/lon is fine,
	// and so are zero temps/cells when those groups are missing.
	e.Has, e.Lat, e.SpeedKmh, e.PackTempMaxC, e.CellVMinMv = 0, 999, 999, 999, 0
	if r := e.CheckRange(recv); r != nil {
		t.Fatalf("absent groups must not be range-checked: %v", r)
	}
	// Late (buffered replay) is fine; slightly ahead (clock skew) is fine.
	e = good()
	e.TsEventMs = recv - 3*dayMs
	if r := e.CheckRange(recv); r != nil {
		t.Fatalf("late event rejected: %v", r)
	}
}

func TestRejectError(t *testing.T) {
	if got := Rejectf(Schema, "missing %s", "vin").Error(); got != "SCHEMA: missing vin" {
		t.Fatal(got)
	}
	if len(Reasons) != 11 {
		t.Fatalf("%d reasons, the contract has 11", len(Reasons))
	}
}
