// Package session detects charge and drive sessions per vehicle (event-sourced: a START and an END
// record per session) and integrates them for SoH estimation in battery-intel.
//
//	charge: starts on PLUG_IN, or charge_state ≠ IDLE with current < 0; ends on PLUG_OUT or when
//	        charging stops (charge_state IDLE / current ≥ 0).
//	drive:  starts on IGN_ON or speed > 0; ends on IGN_OFF or after IdleEndMs without movement.
//
// ΔAh = ∫ I dt and ΔkWh = ∫ V·I dt by the trapezoidal rule between consecutive samples (sign
// convention: + discharge, − charge, so a charge session has negative ΔAh). Intervals longer than
// MaxGapMs are not integrated and are counted in Gaps (data quality for the SoH estimator).
// The rest voltage before a session is captured when the pack rested (|I| < RestCurrentA) for at
// least RestMinMs. Step is O(1).
package session

import (
	"math"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/alert"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

const (
	MaxGapMs     = 5 * 60_000
	RestMinMs    = 30 * 60_000
	RestCurrentA = 1.0
	IdleEndMs    = 15 * 60_000
)

type Kind string

const (
	Charge Kind = "CHARGE"
	Drive  Kind = "DRIVE"
)

type Session struct {
	ID, VIN            string
	Kind               Kind
	Phase              string // START | END
	StartMs, EndMs     int64
	SoCStart, SoCEnd   float64
	DeltaAh, DeltaKWh  float64
	VRestBefore        float64
	HasRestBefore      bool
	TempMinC, TempMaxC float64
	MaxCRate           float64
	ChargerType        string // AC | DC_FAST (charge)
	DistanceKm         float64
	OdoStartKm         float64
	Gaps               int
	Samples            int
}

type open struct {
	s Session
}

type Tracker struct {
	charge, drive *open
	lastTs        int64
	lastI, lastV  float64
	restSinceMs   int64
	resting       bool
	restV         float64
	lastMoveMs    int64
	have          bool
}

func (t *Tracker) start(kind Kind, ev *event.Event, capAh float64) *open {
	o := &open{Session{Kind: kind, VIN: ev.VIN, Phase: "START", StartMs: ev.TsEventMs, SoCStart: ev.SoCPct, OdoStartKm: ev.OdoKm,
		TempMinC: math.Inf(1), TempMaxC: math.Inf(-1)}}
	o.s.ID = alert.ID(ev.VIN, string(kind), ev.TsEventMs)
	if t.resting && ev.TsEventMs-t.restSinceMs >= RestMinMs {
		o.s.VRestBefore, o.s.HasRestBefore = t.restV, true
	}
	if kind == Charge {
		o.s.ChargerType = "AC"
		if ev.ChargeState == 3 {
			o.s.ChargerType = "DC_FAST"
		}
	}
	o.accumulate(ev, capAh)
	return o
}

func (o *open) accumulate(ev *event.Event, capAh float64) {
	o.s.Samples++
	if ev.Has&event.HasTemp != 0 {
		o.s.TempMinC = math.Min(o.s.TempMinC, ev.PackTempMinC)
		o.s.TempMaxC = math.Max(o.s.TempMaxC, ev.PackTempMaxC)
	}
	if capAh > 0 {
		o.s.MaxCRate = math.Max(o.s.MaxCRate, math.Abs(ev.PackCurrentA)/capAh)
	}
}

func (o *open) end(ev *event.Event) Session {
	s := o.s
	s.Phase, s.EndMs, s.SoCEnd = "END", ev.TsEventMs, ev.SoCPct
	s.DistanceKm = math.Max(0, ev.OdoKm-s.OdoStartKm)
	if math.IsInf(s.TempMinC, 1) {
		s.TempMinC, s.TempMaxC = 0, 0
	}
	return s
}

// Step feeds one event (in per-VIN event-time order) and returns session START/END records.
// capAh is the pack's nominal capacity in Ah (for C-rate); 0 if unknown.
func (t *Tracker) Step(ev *event.Event, capAh float64) []Session {
	if t.have && ev.TsEventMs < t.lastTs {
		return nil // out of order: sessions need monotonic time
	}
	var out []Session
	dt := ev.TsEventMs - t.lastTs
	integrate := t.have && dt > 0 && dt <= MaxGapMs
	hours := float64(dt) / 3_600_000
	for _, o := range []*open{t.charge, t.drive} {
		if o == nil {
			continue
		}
		switch {
		case integrate:
			o.s.DeltaAh += (t.lastI + ev.PackCurrentA) / 2 * hours
			o.s.DeltaKWh += (t.lastV*t.lastI + ev.PackVoltageV*ev.PackCurrentA) / 2 * hours / 1000
		case t.have && dt > MaxGapMs:
			o.s.Gaps++
		}
		o.accumulate(ev, capAh)
	}

	// charge
	charging := ev.ChargeState != event.ChargeIdle && ev.PackCurrentA < 0
	if t.charge == nil && (ev.Evt == event.EvtPlugIn && ev.PackCurrentA < 0 || charging) {
		t.charge = t.start(Charge, ev, capAh)
		out = append(out, t.charge.s)
	} else if t.charge != nil && (ev.Evt == event.EvtPlugOut || !charging) {
		out = append(out, t.charge.end(ev))
		t.charge = nil
	}

	// drive
	moving := ev.Has&event.HasGPS != 0 && ev.SpeedKmh > 0
	if moving {
		t.lastMoveMs = ev.TsEventMs
	}
	if t.drive == nil && (ev.Evt == event.EvtIgnOn || moving) {
		t.drive = t.start(Drive, ev, capAh)
		t.lastMoveMs = ev.TsEventMs
		out = append(out, t.drive.s)
	} else if t.drive != nil && (ev.Evt == event.EvtIgnOff || ev.TsEventMs-t.lastMoveMs >= IdleEndMs) {
		out = append(out, t.drive.end(ev))
		t.drive = nil
	}

	// rest tracking (for the next session's OCV reference)
	if math.Abs(ev.PackCurrentA) < RestCurrentA {
		if !t.resting {
			t.restSinceMs, t.resting = ev.TsEventMs, true
		}
		t.restV = ev.PackVoltageV
	} else {
		t.resting = false
	}

	t.lastTs, t.lastI, t.lastV, t.have = ev.TsEventMs, ev.PackCurrentA, ev.PackVoltageV, true
	return out
}

// Open reports whether a charge / drive session is in progress.
func (t *Tracker) Open() (charge, drive bool) { return t.charge != nil, t.drive != nil }
