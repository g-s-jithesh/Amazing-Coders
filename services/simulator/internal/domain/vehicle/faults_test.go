package vehicle

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/fault"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

type window struct{ tempDelta, imbalance, isolation, auxParked, interlockOpen, n, nParked float64 }

func (w *window) add(s telemetry.Sample, m Mode) {
	w.n++
	w.tempDelta += float64(s.PackTempMaxC - s.AmbientC)
	w.imbalance += float64(s.CellVMaxMv - s.CellVMinMv)
	w.isolation += float64(s.IsolationKohm)
	if !s.HVInterlockOK {
		w.interlockOpen++
	}
	if m == Parked || (m == Charging && s.ChargePowerKW == 0) {
		w.nParked++
		w.auxParked += float64(s.Aux12vV)
	}
}

func (w window) mean(x float64) float64       { return x / w.n }
func (w window) meanParked(x float64) float64 { return x / math.Max(1, w.nParked) }

// CLAUDE.md simulator invariant 4: every fault's precursor is visible in telemetry before its DTC,
// the DTC fires exactly at dtc_ts, persists until repair, and then clears.
func TestFaultPrecursorsAndDTCLifecycle(t *testing.T) {
	const precursor = 72 * time.Hour
	const dt = 10.0
	for _, spec := range fault.Catalogue[1:] {
		t.Run(spec.Name, func(t *testing.T) {
			v := newVehicle(6*60, 14*60, 0.8, 11)
			v.InjectFault(spec.Kind, t0.UnixMilli(), precursor)
			truth := v.DrainTruth()
			if len(truth) != 1 || truth[0].Fault != spec.Name || !truth[0].Injected ||
				truth[0].DTCMs-truth[0].PrecursorStartMs != precursor.Milliseconds() ||
				truth[0].RepairMs-truth[0].DTCMs != fault.RepairAfter.Milliseconds() {
				t.Fatalf("truth = %+v", truth)
			}
			dtcMs, repairMs := truth[0].DTCMs, truth[0].RepairMs

			var early, late window
			raisedAt := int64(-1)
			total := int((precursor + fault.RepairAfter + 2*time.Hour).Seconds() / dt)
			for i := 0; i < total; i++ {
				now := t0.Add(time.Duration(float64(i) * dt * float64(time.Second)))
				v.Step(tickAt(now, dt), 28)
				ms := now.UnixMilli()
				for first := true; first || v.PendingEvents() > 0; first = false {
					s := v.Sample(ms)
					if s.Evt == telemetry.EvtDTCRaised {
						raisedAt = ms
					}
					hasDTC := len(s.DTC) > 0
					if hasDTC != (ms >= dtcMs && ms < repairMs) {
						t.Fatalf("t=%v: DTC present=%v (dtc=%d repair=%d)", now, hasDTC, dtcMs, repairMs)
					}
					if hasDTC && !slices.Equal(s.DTC, spec.DTC) {
						t.Fatalf("DTC %v, want %v", s.DTC, spec.DTC)
					}
					switch {
					case ms < t0.Add(6*time.Hour).UnixMilli():
						early.add(s, v.Mode)
					case ms >= dtcMs-6*3600*1000 && ms < dtcMs:
						late.add(s, v.Mode)
					}
				}
			}
			if raisedAt != dtcMs {
				t.Fatalf("DTC_RAISED at %d, want %d", raisedAt, dtcMs)
			}
			if k, _ := v.ActiveFault(); k != fault.None {
				t.Fatalf("fault %v still active after repair", k)
			}

			t.Logf("early→late: tempΔ %.1f→%.1f °C, imbalance %.0f→%.0f mV, isolation %.0f→%.0f kΩ, interlock opens %.0f→%.0f, parked 12V %.2f→%.2f V",
				early.mean(early.tempDelta), late.mean(late.tempDelta), early.mean(early.imbalance), late.mean(late.imbalance),
				early.mean(early.isolation), late.mean(late.isolation), early.interlockOpen, late.interlockOpen,
				early.meanParked(early.auxParked), late.meanParked(late.auxParked))
			switch spec.Kind {
			case fault.CoolingDegradation:
				if e, l := early.mean(early.tempDelta), late.mean(late.tempDelta); l < e+10 {
					t.Errorf("temp delta early %.1f late %.1f: precursor too weak", e, l)
				}
			case fault.CellDrift:
				if e, l := early.mean(early.imbalance), late.mean(late.imbalance); l < e+60 {
					t.Errorf("imbalance early %.0f late %.0f mV", e, l)
				}
			case fault.InsulationWear:
				if e, l := early.mean(early.isolation), late.mean(late.isolation); l > e/5 {
					t.Errorf("isolation early %.0f late %.0f kΩ", e, l)
				}
			case fault.ConnectorIssue:
				if late.interlockOpen <= early.interlockOpen || late.interlockOpen < 3 {
					t.Errorf("interlock opens early %.0f late %.0f", early.interlockOpen, late.interlockOpen)
				}
			case fault.WeakAux:
				if e, l := early.meanParked(early.auxParked), late.meanParked(late.auxParked); l > e-0.8 {
					t.Errorf("parked 12 V early %.2f late %.2f", e, l)
				}
			}
		})
	}
}

func TestRandomFaultOnsetFromHazard(t *testing.T) {
	v := newVehicle(6*60, 14*60, 0.8, 12)
	v.FaultRatePerYear = 1
	v.nextFaultMs = t0.UnixMilli() // due now
	v.Step(tickAt(t0, 1), 28)
	k, raised := v.ActiveFault()
	tr := v.DrainTruth()
	if k == fault.None || raised || len(tr) != 1 || tr[0].Injected {
		t.Fatalf("kind=%v raised=%v truth=%+v", k, raised, tr)
	}
	spec := fault.Catalogue[k]
	if d := time.Duration(tr[0].DTCMs-tr[0].PrecursorStartMs) * time.Millisecond; d < spec.PrecursorMin || d > spec.PrecursorMax {
		t.Fatalf("precursor %v outside %v–%v", d, spec.PrecursorMin, spec.PrecursorMax)
	}
	if v.DrainTruth() != nil {
		t.Fatal("DrainTruth must clear")
	}
}

// Mean time between onsets ≈ 1/λ with λ = rate·(1 + 5·fade); a pack with more fade faults sooner.
func TestHazardScheduling(t *testing.T) {
	meanGapYears := func(soh float64) float64 {
		v := newVehicle(0, 1, 0.5, 13)
		v.FaultRatePerYear = 0.5
		v.Batt.QCal = 1 - soh
		sum := 0.0
		const n = 20000
		for i := 0; i < n; i++ {
			v.ScheduleNextFault(0)
			sum += float64(v.nextFaultMs) / msPerYear
		}
		return sum / n
	}
	if g := meanGapYears(1); math.Abs(g-2) > 0.1 { // 1/0.5
		t.Errorf("mean gap at SoH 100%% = %.3f y, want 2", g)
	}
	if g := meanGapYears(0.8); math.Abs(g-1) > 0.05 { // 1/(0.5·2)
		t.Errorf("mean gap at SoH 80%% = %.3f y, want 1", g)
	}
	v := newVehicle(0, 1, 0.5, 14)
	v.ScheduleNextFault(0) // rate 0 → never
	if v.nextFaultMs != math.MaxInt64 {
		t.Error("zero fault rate must never schedule")
	}
}

// Age-sensitive faults (cell drift, insulation wear) become more likely as the pack fades.
func TestKindMixShiftsWithAge(t *testing.T) {
	share := func(soh float64) float64 {
		v := newVehicle(0, 1, 0.5, 15)
		v.Batt.QCal = 1 - soh
		n, aged := 20000, 0
		for i := 0; i < n; i++ {
			if k := v.pickKind(); fault.Catalogue[k].AgeSensitive {
				aged++
			}
		}
		return float64(aged) / float64(n)
	}
	if young, old := share(1), share(0.75); old < young+0.15 {
		t.Errorf("age-sensitive share young %.2f old %.2f", young, old)
	}
}
