package vehicle

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/battery"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vin"
)

var testVIN, _ = vin.Build("0KC", "DV45N", 2024, 'C', 1)

var t0 = time.Date(2026, 9, 1, 0, 0, 0, 0, env.IST) // midnight IST

func newVehicle(depart, ret int, soc float64, seed uint64) *Vehicle {
	spec := Spec{
		VIN: testVIN, OEM: "oem_c", City: "Chennai",
		WhPerKm: 190, MaxACKW: 11,
		HomeLat: 13.05, HomeLon: 80.25, CityLat: 13.0827, CityLon: 80.2707, CitySpanDeg: 0.10,
		DepartMin: depart, ReturnMin: ret, PlannedKm: 120, IsolationBaseKohm: 2500,
	}
	pack := battery.NewParams(battery.NMC, 45, 350, 1)
	return New(spec, pack, battery.State{SoC: soc, TempC: 28, CoolingEff: 1}, rand.New(rand.NewPCG(seed, 1)))
}

func tickAt(t time.Time, dt float64) Tick {
	ist := t.In(env.IST)
	y, m, d := ist.Date()
	return Tick{NowMs: t.UnixMilli(), Dt: dt, MinIST: env.MinuteOfDayIST(t), DayIST: time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400}
}

type run struct {
	samples []telemetry.Sample
	modes   []Mode
}

// simulate steps at dt seconds and samples every tick.
func simulate(v *Vehicle, from time.Time, hours float64, dt float64) run {
	var r run
	n := int(hours * 3600 / dt)
	for i := 0; i < n; i++ {
		now := from.Add(time.Duration(float64(i) * dt * float64(time.Second)))
		v.Step(tickAt(now, dt), 28)
		r.samples = append(r.samples, v.Sample(now.UnixMilli()))
		r.modes = append(r.modes, v.Mode)
	}
	return r
}

func minuteOf(s telemetry.Sample) int { return env.MinuteOfDayIST(time.UnixMilli(s.TsEventMs)) }

func TestMorningShiftDayCycle(t *testing.T) {
	v := newVehicle(6*60, 14*60, 0.6, 1)
	r := simulate(v, t0, 48, 1)

	var ignOn, ignOff, plugIn, plugOut []int
	for i, s := range r.samples {
		switch s.Evt {
		case telemetry.EvtIgnOn:
			ignOn = append(ignOn, i)
		case telemetry.EvtIgnOff:
			ignOff = append(ignOff, i)
		case telemetry.EvtPlugIn:
			plugIn = append(plugIn, i)
		case telemetry.EvtPlugOut:
			plugOut = append(plugOut, i)
		}
		if s.SoCPct < 0 || s.SoCPct > 100 {
			t.Fatalf("SoC %.2f out of range", s.SoCPct)
		}
		m := r.modes[i]
		if (m == Driving || m == Returning) && s.PackCurrentA <= 0 {
			t.Fatalf("sample %d driving with current %.2f (must discharge)", i, s.PackCurrentA)
		}
		if s.ChargePowerKW > 0 && s.PackCurrentA >= 0 {
			t.Fatalf("sample %d charging with current %.2f (must be negative)", i, s.PackCurrentA)
		}
		if s.ChargePowerKW > 0 != (s.ChargeState == telemetry.ChargeAC) {
			t.Fatalf("sample %d charge_state %v with %.1f kW", i, s.ChargeState, s.ChargePowerKW)
		}
	}
	if len(ignOn) != 2 || len(ignOff) != 2 {
		t.Fatalf("want one shift per day for 2 days: ign_on=%d ign_off=%d", len(ignOn), len(ignOff))
	}
	for _, i := range ignOn {
		if m := minuteOf(r.samples[i]); m != 6*60 {
			t.Fatalf("departed at minute %d, want 360", m)
		}
	}
	// Charge on arrival: the plug-in follows each ignition-off within a couple of samples.
	for k, off := range ignOff {
		if k >= len(plugIn) || plugIn[k]-off > 2 {
			t.Fatalf("no plug-in right after arrival #%d (off=%d plugIn=%v)", k, off, plugIn)
		}
	}
	// Each departure unplugs first (it was charging); both events fire in one tick and pop on consecutive samples.
	if len(plugOut) != 2 || plugOut[0] != ignOn[0]-1 || plugOut[1] != ignOn[1]-1 {
		t.Fatalf("expected PLUG_OUT just before second IGN_ON: plugOut=%v ignOn=%v", plugOut, ignOn)
	}
	// Drove at least the planned distance on day 1 and returned by shift end + return trip.
	trip := r.samples[ignOff[0]].OdoKm - r.samples[ignOn[0]].OdoKm
	if trip < v.PlannedKm*0.9 {
		t.Fatalf("day-1 trip %.1f km < planned %.0f", trip, v.PlannedKm)
	}
	// Fully charged before the next departure.
	if soc := r.samples[ignOn[1]-1].SoCPct; soc < 99 {
		t.Fatalf("SoC %.1f%% at second departure, want ~100", soc)
	}
}

func TestNightShiftCrossesMidnight(t *testing.T) {
	v := newVehicle(22*60, 6*60, 0.9, 2)
	r := simulate(v, t0, 72, 5)
	var starts []int
	for _, s := range r.samples {
		if s.Evt == telemetry.EvtIgnOn {
			starts = append(starts, minuteOf(s))
		}
	}
	// Sim starts at 00:00 inside the night shift, so that shift starts immediately; then 22:00 on each of 3 days.
	if len(starts) != 4 || starts[0] != 0 {
		t.Fatalf("want 4 shift starts (first at sim start), got %v", starts)
	}
	for _, m := range starts[1:] {
		if m != 22*60 {
			t.Fatalf("night shift started at minute %d", m)
		}
	}
	// It is driving across midnight on at least one night.
	crossed := false
	for i, s := range r.samples {
		if m := minuteOf(s); m >= 0 && m < 30 && r.modes[i] == Driving {
			crossed = true
			break
		}
	}
	if !crossed {
		t.Fatal("never observed driving just after midnight")
	}
}

func TestStrandedThenTowed(t *testing.T) {
	v := newVehicle(0, 1439, 0.0001, 3)
	v.Mode, v.Lat, v.Lon = Driving, 13.15, 80.35
	v.depart(tickAt(t0, 1))
	v.nEvt = 0
	r := simulate(v, t0, 3, 1)
	strandedAt, parkedAt := -1, -1
	for i, m := range r.modes {
		if m == Stranded && strandedAt < 0 {
			strandedAt = i
			if r.samples[i].Evt != telemetry.EvtIgnOff {
				t.Fatalf("stranding should emit IGN_OFF, got %v", r.samples[i].Evt)
			}
		}
		if strandedAt >= 0 && m != Stranded && parkedAt < 0 {
			parkedAt = i
		}
	}
	if strandedAt < 0 || parkedAt < 0 {
		t.Fatalf("stranded=%d recovered=%d", strandedAt, parkedAt)
	}
	if got := parkedAt - strandedAt; got < 7200 || got > 7202 {
		t.Fatalf("towed after %d s, want 7200", got)
	}
	if s := r.samples[parkedAt]; math.Abs(s.Lat-v.HomeLat) > 1e-9 || math.Abs(s.Lon-v.HomeLon) > 1e-9 {
		t.Fatalf("not towed home: %v,%v", s.Lat, s.Lon)
	}
}

// Sampling rate must not change the trajectory (Sample never consumes RNG).
func TestSamplingDoesNotPerturbTrajectory(t *testing.T) {
	a, b := newVehicle(6*60, 14*60, 0.7, 9), newVehicle(6*60, 14*60, 0.7, 9)
	for i := 0; i < 12*3600; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		tk := tickAt(now, 1)
		a.Step(tk, 30)
		b.Step(tk, 30)
		a.Sample(tk.NowMs)
		if i%60 == 0 {
			b.Sample(tk.NowMs)
		}
	}
	if a.Lat != b.Lat || a.Lon != b.Lon || a.OdoKm != b.OdoKm || a.Batt != b.Batt {
		t.Fatal("trajectories diverged with different sampling rates")
	}
}

func TestEventQueueOrderAndBound(t *testing.T) {
	v := newVehicle(0, 1, 0.5, 4)
	for _, e := range []telemetry.EventType{telemetry.EvtPlugOut, telemetry.EvtIgnOn, telemetry.EvtHarshBrake, telemetry.EvtIgnOff, telemetry.EvtPlugIn} {
		v.push(e)
	}
	want := []telemetry.EventType{telemetry.EvtPlugOut, telemetry.EvtIgnOn, telemetry.EvtHarshBrake, telemetry.EvtIgnOff, telemetry.EvtPeriodic}
	for i, w := range want {
		if got := v.Sample(int64(i)).Evt; got != w {
			t.Fatalf("pop %d = %v, want %v", i, got, w)
		}
	}
	if v.PendingEvents() != 0 {
		t.Fatalf("pending = %d after draining", v.PendingEvents())
	}
	if v.seq != 5 {
		t.Fatalf("seq = %d", v.seq)
	}
}

func TestModeString(t *testing.T) {
	if Charging.String() != "CHARGING" || Stranded.String() != "STRANDED" {
		t.Fatal("mode names")
	}
}

func BenchmarkStepAndSample(b *testing.B) {
	v := newVehicle(0, 1439, 0.9, 5)
	for i := 0; i < b.N; i++ {
		now := t0.Add(time.Duration(i) * time.Second)
		tk := Tick{NowMs: now.UnixMilli(), Dt: 1, MinIST: (i / 60) % 1440, DayIST: int64(i / 86400)}
		v.Step(tk, 30)
		_ = v.Sample(tk.NowMs)
		if v.Batt.SoC < 0.15 {
			v.Batt.SoC = 0.9
		}
	}
}
