// Package vehicle is the per-vehicle state machine: parked → driving → returning → charging
// (plus stranded → towed). Depot charging is the naive baseline "charge on arrival at max AC
// power to 100 %", which the dispatch optimiser is measured against.
//
// Step consumes the vehicle's own RNG (seeded from hash(seed, VIN)), so a vehicle's trajectory is
// independent of shard layout and of how often it is sampled. Sample never touches the RNG.
// Step and Sample are O(1) and allocation-free.
package vehicle

import (
	"math"
	"math/rand/v2"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/battery"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/energy"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/fault"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

type Mode uint8

const (
	Parked Mode = iota
	Driving
	Returning
	Charging
	Stranded
)

func (m Mode) String() string {
	return [...]string{"PARKED", "DRIVING", "RETURNING", "CHARGING", "STRANDED"}[m]
}

const (
	kmPerDegLat     = 111.2
	arriveKm        = 0.15
	lowSoC          = 0.10 // head home below this
	minDepartSoC    = 0.20 // keep charging past shift start until this
	chargeEff       = 0.92 // grid → pack
	taperFromSoC    = 0.80
	depotACCapKW    = 22.0
	towAfterMs      = 2 * 3600 * 1000
	maxAccelKmhPerS = 7.2 // ≈ 2 m/s²
	minCruiseKmh    = 15.0
	cruiseSpreadKmh = 40.0
)

// Spec is the static description of a vehicle, from master data.
type Spec struct {
	VIN, OEM, City                string
	WhPerKm, MaxACKW              float64
	HomeLat, HomeLon              float64
	CityLat, CityLon, CitySpanDeg float64
	DepartMin, ReturnMin          int // IST minute of day
	PlannedKm                     float64
	IsolationBaseKohm             float64
	FaultRatePerYear              float64 // expected random fault onsets per vehicle-year at SoH 100 %
}

// Tick carries the per-tick clock, computed once for the whole fleet.
type Tick struct {
	NowMs  int64
	Dt     float64 // seconds
	MinIST int     // 0..1439
	DayIST int64   // days since epoch in IST
}

type Vehicle struct {
	Spec
	Pack battery.Params
	Batt battery.State
	Mode Mode

	Lat, Lon, SpeedKmh, OdoKm float64
	CurrentA, ChargeKW        float64
	AmbientC                  float64

	rng                                   *rand.Rand
	wpLat, wpLon, targetKmh, tripKm       float64
	nextTargetMs, nextStopMs, stopUntilMs int64
	strandedAtMs                          int64
	lastShiftDay                          int64
	seq                                   uint64
	evts                                  [4]telemetry.EventType // pending, oldest first
	nEvt                                  int

	fault            activeFault
	fx               fault.Effects
	nextFaultMs      int64
	interlockOpen    bool
	interlockCloseMs int64
	truth            []FaultTruth
}

// New places the vehicle parked at its home depot.
func New(spec Spec, pack battery.Params, batt battery.State, rng *rand.Rand) *Vehicle {
	return &Vehicle{Spec: spec, Pack: pack, Batt: batt, rng: rng, Lat: spec.HomeLat, Lon: spec.HomeLon,
		lastShiftDay: math.MinInt64, nextFaultMs: math.MaxInt64, fx: fault.Healthy}
}

func (v *Vehicle) push(e telemetry.EventType) {
	if v.nEvt < len(v.evts) {
		v.evts[v.nEvt] = e
		v.nEvt++
	}
}

// shiftDay is the IST day on which the shift covering minute m started.
func (v *Vehicle) shiftDay(tk Tick) int64 {
	if v.DepartMin > v.ReturnMin && tk.MinIST < v.ReturnMin {
		return tk.DayIST - 1
	}
	return tk.DayIST
}

// Step advances the vehicle by tk.Dt seconds.
func (v *Vehicle) Step(tk Tick, ambientC float64) {
	v.AmbientC = ambientC
	v.stepFault(tk)
	inShift := env.InShift(tk.MinIST, v.DepartMin, v.ReturnMin)

	switch v.Mode {
	case Parked, Charging:
		if inShift && v.shiftDay(tk) != v.lastShiftDay && v.Batt.SoC >= minDepartSoC {
			v.depart(tk)
		}
	case Driving:
		if !inShift || v.tripKm >= v.PlannedKm || v.Batt.SoC < lowSoC {
			v.Mode, v.wpLat, v.wpLon = Returning, v.HomeLat, v.HomeLon
		}
	case Stranded:
		if tk.NowMs-v.strandedAtMs >= towAfterMs { // towed back to depot
			v.Mode, v.Lat, v.Lon = Parked, v.HomeLat, v.HomeLon
		}
	}

	v.CurrentA, v.ChargeKW = 0, 0
	switch v.Mode {
	case Driving, Returning:
		v.drive(tk)
	case Parked:
		v.SpeedKmh = 0
		if v.Batt.SoC < 0.999 { // not departing (checked above) → charge on arrival
			v.Mode = Charging
			v.push(telemetry.EvtPlugIn)
		}
	case Charging:
		v.charge()
	case Stranded:
		v.SpeedKmh = 0
	}

	v.Pack.Step(&v.Batt, v.CurrentA, ambientC, tk.Dt)

	if v.Batt.SoC <= 0 && (v.Mode == Driving || v.Mode == Returning) {
		v.Mode, v.SpeedKmh, v.strandedAtMs = Stranded, 0, tk.NowMs
		v.push(telemetry.EvtIgnOff)
	}
}

func (v *Vehicle) depart(tk Tick) {
	if v.Mode == Charging {
		v.push(telemetry.EvtPlugOut)
	}
	v.push(telemetry.EvtIgnOn)
	v.Mode, v.tripKm, v.lastShiftDay = Driving, 0, v.shiftDay(tk)
	v.pickWaypoint()
	v.targetKmh = minCruiseKmh + v.rng.Float64()*cruiseSpreadKmh
	v.nextTargetMs = tk.NowMs + int64(60+v.rng.IntN(240))*1000
	v.nextStopMs = tk.NowMs + int64(480+v.rng.IntN(720))*1000
	v.stopUntilMs = 0
}

func (v *Vehicle) pickWaypoint() {
	v.wpLat = v.CityLat + (v.rng.Float64()*2-1)*v.CitySpanDeg
	v.wpLon = v.CityLon + (v.rng.Float64()*2-1)*v.CitySpanDeg
}

func (v *Vehicle) drive(tk Tick) {
	returning := v.Mode == Returning
	if !returning && tk.NowMs >= v.nextStopMs { // delivery stop, 2–6 min
		v.stopUntilMs = tk.NowMs + int64(120+v.rng.IntN(240))*1000
		v.nextStopMs = v.stopUntilMs + int64(480+v.rng.IntN(720))*1000
	}
	if tk.NowMs >= v.nextTargetMs {
		v.targetKmh = minCruiseKmh + v.rng.Float64()*cruiseSpreadKmh
		v.nextTargetMs = tk.NowMs + int64(60+v.rng.IntN(240))*1000
	}
	target := v.targetKmh
	if !returning && tk.NowMs < v.stopUntilMs {
		target = 0
	}
	dv := maxAccelKmhPerS * tk.Dt
	v.SpeedKmh += math.Max(-dv, math.Min(dv, target-v.SpeedKmh))

	if km := v.SpeedKmh * tk.Dt / 3600; km > 0 {
		v.moveToward(km, returning)
		v.OdoKm += km
		v.tripKm += km
	}

	powerW := energy.TractionW(v.WhPerKm, v.SpeedKmh) + energy.HVACW(v.AmbientC) + energy.AuxW
	v.CurrentA = v.Pack.CurrentForPower(&v.Batt, powerW)
}

func (v *Vehicle) moveToward(km float64, returning bool) {
	cosLat := math.Cos(v.Lat * math.Pi / 180)
	dLat, dLon := v.wpLat-v.Lat, v.wpLon-v.Lon
	dist := math.Hypot(dLat, dLon*cosLat) * kmPerDegLat
	if dist <= km+arriveKm {
		v.Lat, v.Lon = v.wpLat, v.wpLon
		if returning {
			v.Mode, v.SpeedKmh = Parked, 0
			v.push(telemetry.EvtIgnOff)
			return
		}
		v.pickWaypoint()
		return
	}
	f := km / dist
	v.Lat += dLat * f
	v.Lon += dLon * f
}

// charge is the baseline: max AC power (capped by the depot charger), tapering above 80 % SoC.
func (v *Vehicle) charge() {
	v.SpeedKmh = 0
	if v.Batt.SoC >= 0.999 {
		return // full: stays plugged, charge_state IDLE
	}
	kw := math.Min(v.MaxACKW, depotACCapKW)
	if v.Batt.SoC > taperFromSoC {
		kw *= 1 - 0.8*(v.Batt.SoC-taperFromSoC)/(1-taperFromSoC)
	}
	v.ChargeKW = kw
	v.CurrentA = v.Pack.CurrentForPower(&v.Batt, -kw*1000*chargeEff)
}

// PendingEvents is the number of events not yet reported. Emitters must drain them right after
// each Step (one Sample per event), as real telematics units send event messages immediately
// rather than waiting for the next periodic report.
func (v *Vehicle) PendingEvents() int { return v.nEvt }

// Sample reports the current state as a canonical sample. It pops at most one pending event.
func (v *Vehicle) Sample(nowMs int64) telemetry.Sample {
	v.seq++
	evt := telemetry.EvtPeriodic
	if v.nEvt > 0 {
		evt = v.evts[0]
		copy(v.evts[:], v.evts[1:v.nEvt])
		v.nEvt--
	}
	b, p := &v.Batt, &v.Pack
	cRate := math.Abs(v.CurrentA) / p.CapacityAh
	spread := 1 + 2*cRate
	volts := p.TerminalV(b, v.CurrentA)
	cellMv := volts / float64(p.Series) * 1000
	imbMv := 8 + 60*(1-b.SoH()) + 10*cRate + v.fx.ExtraImbalanceMv

	aux := 12.6 - v.fx.AuxSagV
	if v.Mode == Driving || v.Mode == Returning || v.ChargeKW > 0 {
		aux = 13.9 - 0.3*v.fx.AuxSagV // DC-DC converter active
	} else if v.Mode == Stranded {
		aux = 12.2 - v.fx.AuxSagV
	}
	var dtc []string
	if v.fault.raised {
		dtc = fault.Catalogue[v.fault.kind].DTC // shared, read-only
	}
	cs := telemetry.ChargeIdle
	if v.ChargeKW > 0 {
		cs = telemetry.ChargeAC
	}
	return telemetry.Sample{
		VIN: v.VIN, OEM: v.OEM, TsEventMs: nowMs, Seq: v.seq,
		Lat: v.Lat, Lon: v.Lon, SpeedKmh: float32(v.SpeedKmh), OdoKm: v.OdoKm,
		SoCPct: float32(b.SoC * 100), PackVoltageV: float32(volts), PackCurrentA: float32(v.CurrentA),
		PackTempMinC: float32(b.TempC - 0.4*spread), PackTempMaxC: float32(b.TempC + 0.6*spread),
		CellVMinMv: uint32(cellMv - imbMv/2), CellVMaxMv: uint32(cellMv + imbMv/2),
		IsolationKohm: float32(v.IsolationBaseKohm * v.fx.IsolationFactor), HVInterlockOK: !v.interlockOpen,
		Aux12vV: float32(aux), AmbientC: float32(v.AmbientC),
		ChargeState: cs, ChargePowerKW: float32(v.ChargeKW), DTC: dtc, Evt: evt, SchemaVersion: telemetry.SchemaVersion,
	}
}
