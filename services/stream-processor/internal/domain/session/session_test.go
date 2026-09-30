package session

import (
	"math"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

func sample(tSec int64, amps float64) *event.Event {
	e := &event.Event{VIN: "V", TsEventMs: tSec * 1000, PackCurrentA: amps, PackVoltageV: 350, ChargeState: 1, SoCPct: 50, Evt: 1,
		Has: event.HasTemp, PackTempMinC: 25, PackTempMaxC: 28}
	if amps < 0 {
		e.ChargeState = 2
	}
	return e
}

func phases(ss []Session) (starts, ends []Session) {
	for _, s := range ss {
		if s.Phase == "START" {
			starts = append(starts, s)
		} else {
			ends = append(ends, s)
		}
	}
	return
}

// Constant −50 A for 1 h = −50 Ah; a linear ramp integrates exactly with trapezoids.
func TestChargeDeltaAhAnalytic(t *testing.T) {
	var tr Tracker
	var all []Session
	for s := int64(0); s < 1800; s += 10 {
		all = append(all, tr.Step(sample(s, 0), 128)...) // 30 min rest before
	}
	for s := int64(1800); s <= 1800+3600; s += 10 {
		all = append(all, tr.Step(sample(s, -50), 128)...)
	}
	all = append(all, tr.Step(sample(1800+3610, 0), 128)...)
	st, en := phases(all)
	if len(st) != 1 || len(en) != 1 || st[0].Kind != Charge || en[0].ID != st[0].ID {
		t.Fatalf("sessions %+v", all)
	}
	e := en[0]
	// −50 A from 1800 s to 5400 s, then a 10 s trapezoid ramping to 0 A.
	want := -50.0 - 50.0/2*10/3600
	if math.Abs(e.DeltaAh-want) > 1e-9 {
		t.Fatalf("ΔAh %.6f, want %.6f", e.DeltaAh, want)
	}
	if !st[0].HasRestBefore || st[0].VRestBefore != 350 || e.ChargerType != "AC" || math.Abs(e.MaxCRate-50.0/128) > 1e-9 {
		t.Fatalf("rest/charger/c-rate wrong: %+v", st[0])
	}
	if math.Abs(e.DeltaKWh-want*350/1000) > 1e-9 {
		t.Fatalf("ΔkWh %.6f", e.DeltaKWh)
	}

	var tr2 Tracker
	var got float64
	for s := int64(0); s <= 600; s += 10 {
		for _, x := range tr2.Step(sample(s, -float64(s)/10-1), 0) { // I = −(1 + t/10), charge from t=0
			if x.Phase == "END" {
				got = x.DeltaAh
			}
		}
	}
	for _, x := range tr2.Step(sample(610, 0), 0) {
		got = x.DeltaAh
	}
	// ∫0..600 −(1+t/10) dt s = −(600 + 18000) As, plus the last trapezoid (−61 → 0 over 10 s = −305 As)
	if want := -(600.0 + 18000 + 305) / 3600; math.Abs(got-want) > 1e-9 {
		t.Fatalf("ramp ΔAh %.9f, want %.9f", got, want)
	}
}

func TestGapsAreNotIntegrated(t *testing.T) {
	var tr Tracker
	tr.Step(sample(0, -50), 0)
	tr.Step(sample(10, -50), 0)
	tr.Step(sample(10+600, -50), 0) // 10 min dropout
	var end Session
	for _, x := range tr.Step(sample(620, 0), 0) {
		end = x
	}
	want := -50.0*10/3600 - 25.0*10/3600 // first 10 s + final ramp only
	if end.Gaps != 1 || math.Abs(end.DeltaAh-want) > 1e-9 {
		t.Fatalf("gaps=%d ΔAh=%.6f want %.6f", end.Gaps, end.DeltaAh, want)
	}
}

func TestDriveSession(t *testing.T) {
	var tr Tracker
	mk := func(s int64, speed, odo float64, evt int32) *event.Event {
		e := sample(s, 20)
		e.SpeedKmh, e.OdoKm, e.Evt, e.Has = speed, odo, evt, event.HasGPS
		return e
	}
	var all []Session
	all = append(all, tr.Step(mk(0, 0, 100, event.EvtIgnOn), 0)...)
	all = append(all, tr.Step(mk(60, 40, 100.6, 1), 0)...)
	all = append(all, tr.Step(mk(120, 0, 101.2, event.EvtIgnOff), 0)...)
	st, en := phases(all)
	if len(st) != 1 || len(en) != 1 || en[0].Kind != Drive || math.Abs(en[0].DistanceKm-1.2) > 1e-9 || en[0].DeltaAh <= 0 {
		t.Fatalf("drive: %+v", all)
	}
	if c, d := tr.Open(); c || d {
		t.Fatal("nothing should be open")
	}
	// Idle timeout ends a drive without IGN_OFF.
	var tr2 Tracker
	tr2.Step(mk(0, 30, 0, 1), 0)
	var ended bool
	for s := int64(60); s <= 60+IdleEndMs/1000; s += 60 {
		for _, x := range tr2.Step(mk(s, 0, 0.5, 1), 0) {
			ended = ended || x.Phase == "END"
		}
	}
	if !ended {
		t.Fatal("drive must end after 15 min without movement")
	}
}

func TestOutOfOrderIgnoredAndDCFast(t *testing.T) {
	var tr Tracker
	e := sample(100, -150)
	e.ChargeState = 3
	st := tr.Step(e, 128)
	if len(st) != 1 || st[0].ChargerType != "DC_FAST" {
		t.Fatalf("dc start: %+v", st)
	}
	if out := tr.Step(sample(50, 0), 128); out != nil {
		t.Fatal("out-of-order sample must be ignored")
	}
	if c, _ := tr.Open(); !c {
		t.Fatal("charge must still be open")
	}
}
