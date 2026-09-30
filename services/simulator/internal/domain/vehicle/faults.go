package vehicle

import (
	"math"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/fault"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

const msPerYear = 365.25 * 86400 * 1000

// FaultTruth is one injected fault, as written to the ground-truth sink (never to the pipeline).
type FaultTruth struct {
	VIN              string
	Fault            string
	DTC              string // comma-separated codes raised at DTCMs
	PrecursorStartMs int64
	DTCMs            int64
	RepairMs         int64
	Injected         bool // true for --demo-inject, false for the random hazard process
}

type activeFault struct {
	kind                     fault.Kind
	startMs, dtcMs, repairMs int64
	raised                   bool
}

// ScheduleNextFault draws the next fault onset from a Poisson process whose rate grows with pack
// fade: λ = FaultRatePerYear × (1 + 5·fade). Called at build and after each repair.
func (v *Vehicle) ScheduleNextFault(nowMs int64) {
	if v.FaultRatePerYear <= 0 {
		v.nextFaultMs = math.MaxInt64
		return
	}
	lambda := v.FaultRatePerYear * (1 + 5*(1-v.Batt.SoH()))
	v.nextFaultMs = nowMs + int64(v.rng.ExpFloat64()/lambda*msPerYear)
}

func (v *Vehicle) pickKind() fault.Kind {
	fade := 1 - v.Batt.SoH()
	w := func(s fault.Spec) float64 {
		if s.AgeSensitive {
			return s.Weight * (1 + 10*fade)
		}
		return s.Weight
	}
	total := 0.0
	for _, s := range fault.Catalogue[1:] {
		total += w(s)
	}
	x := v.rng.Float64() * total
	for _, s := range fault.Catalogue[1:] {
		if x -= w(s); x < 0 {
			return s.Kind
		}
	}
	return fault.Catalogue[len(fault.Catalogue)-1].Kind
}

// InjectFault starts fault k now with the given precursor length, replacing any active fault.
// Used by --demo-inject (short precursor) and by tests.
func (v *Vehicle) InjectFault(k fault.Kind, nowMs int64, precursor time.Duration) {
	v.startFault(k, nowMs, precursor, true)
}

func (v *Vehicle) startFault(k fault.Kind, nowMs int64, precursor time.Duration, injected bool) {
	dtc := nowMs + precursor.Milliseconds()
	v.fault = activeFault{kind: k, startMs: nowMs, dtcMs: dtc, repairMs: dtc + fault.RepairAfter.Milliseconds()}
	spec := fault.Catalogue[k]
	codes := spec.DTC[0]
	for _, c := range spec.DTC[1:] {
		codes += "," + c
	}
	v.truth = append(v.truth, FaultTruth{VIN: v.VIN, Fault: spec.Name, DTC: codes,
		PrecursorStartMs: nowMs, DTCMs: dtc, RepairMs: v.fault.repairMs, Injected: injected})
}

// DrainTruth returns and clears ground-truth fault records produced since the last call.
func (v *Vehicle) DrainTruth() []FaultTruth {
	out := v.truth
	v.truth = nil
	return out
}

// ActiveFault reports the current fault kind (None if healthy) and whether its DTC is raised.
func (v *Vehicle) ActiveFault() (fault.Kind, bool) { return v.fault.kind, v.fault.raised }

// stepFault advances the fault lifecycle (onset → precursor → DTC → repair) and applies effects.
func (v *Vehicle) stepFault(tk Tick) {
	f := &v.fault
	switch {
	case f.kind == fault.None && tk.NowMs >= v.nextFaultMs:
		s := fault.Catalogue[v.pickKind()]
		span := s.PrecursorMax - s.PrecursorMin
		v.startFault(s.Kind, tk.NowMs, s.PrecursorMin+time.Duration(v.rng.Int64N(int64(span)+1)), false)
	case f.kind != fault.None && !f.raised && tk.NowMs >= f.dtcMs:
		f.raised = true
		v.push(telemetry.EvtDTCRaised)
	case f.raised && tk.NowMs >= f.repairMs:
		*f = activeFault{}
		v.ScheduleNextFault(tk.NowMs)
	}

	v.fx = fault.Healthy
	if f.kind != fault.None {
		v.fx = fault.EffectsAt(f.kind, fault.Progress(f.startMs, f.dtcMs, tk.NowMs))
	}
	v.Batt.CoolingEff = v.fx.CoolingEff
	v.Batt.ExtraHeatW = v.fx.ExtraHeatRiseC / v.Pack.ThermResKPerW

	if v.fx.InterlockFlapPerH > 0 {
		if v.interlockOpen && tk.NowMs >= v.interlockCloseMs {
			v.interlockOpen = false
		} else if !v.interlockOpen && v.rng.Float64() < v.fx.InterlockFlapPerH*tk.Dt/3600 {
			v.interlockOpen, v.interlockCloseMs = true, tk.NowMs+int64(1+v.rng.IntN(5))*1000
		}
	} else {
		v.interlockOpen = false
	}
}
