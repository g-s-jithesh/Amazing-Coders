package rules

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

func loadCfg(t *testing.T) Config {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	c.DTCSeverity = map[string]string{"P0A7E": "critical", "U0111": "medium"}
	return c
}

const vin = "0KCDV45N9RC000001"

// ev returns a healthy, driving sample at t seconds.
func ev(t int64) *event.Event {
	return &event.Event{VIN: vin, TsEventMs: t * 1000, SoCPct: 70, PackVoltageV: 360, PackCurrentA: 20,
		PackTempMinC: 30, PackTempMaxC: 32, CellVMinMv: 3700, CellVMaxMv: 3712, IsolationKohm: 2500,
		HVInterlockOK: true, Aux12vV: 13.9, AmbientC: 30, ChargeState: 1, SpeedKmh: 40, Lat: 13.08, Lon: 80.27,
		Evt: 1, Has: event.HasGPS | event.HasTemp | event.HasCells}
}

type run struct {
	e *Engine
	v *Vehicle
}

func newRun(t *testing.T) *run { return &run{New(loadCfg(t)), NewVehicle()} }

// feed evaluates events every step seconds from `from` to `to` (inclusive), mutating each with f.
func (r *run) feed(from, to, step int64, f func(*event.Event)) []Alert {
	var out []Alert
	for s := from; s <= to; s += step {
		e := ev(s)
		if f != nil {
			f(e)
		}
		out = append(out, r.e.Eval(r.v, e, nil, s*1000)...)
	}
	return out
}

func only(as []Alert, rule, state string) []Alert {
	var o []Alert
	for _, a := range as {
		if a.RuleID == rule && a.State == state {
			o = append(o, a)
		}
	}
	return o
}

func TestConfigLoadsEveryRule(t *testing.T) {
	c := loadCfg(t)
	for _, id := range []string{ThermalOvertemp, ThermalRiseRate, ThermalDeltaAnomaly, CellImbalance, IsolationLow, HVInterlockOpen, Aux12vLow, ChargeNeeded, DTCRaised, TelemetryStale} {
		if !c.Rules[id].Enabled {
			t.Errorf("rule %s missing or disabled in rules.yaml", id)
		}
	}
}

func TestOvertempThresholdSustainAndHysteresis(t *testing.T) {
	// Just below the critical threshold (55): only the warning level (48) fires.
	r := newRun(t)
	out := r.feed(0, 120, 10, func(e *event.Event) { e.PackTempMaxC = 54.9 })
	if w, c := only(out, ThermalOvertemp, "FIRING"), 0; len(w) != 1 || w[0].Severity != "warning" || c != 0 {
		t.Fatalf("54.9 °C: %+v", out)
	}
	// At threshold, held 30 s → critical fires at t=30 with window start 0.
	r = newRun(t)
	out = r.feed(0, 60, 10, func(e *event.Event) { e.PackTempMaxC = 55 })
	var crit []Alert
	for _, a := range only(out, ThermalOvertemp, "FIRING") {
		if a.Severity == "critical" {
			crit = append(crit, a)
		}
	}
	if len(crit) != 1 || crit[0].TsMs != 30_000 || crit[0].WindowStartMs != 0 || crit[0].Evidence["value"] != 55 {
		t.Fatalf("critical: %+v", crit)
	}
	// Cools down: resolves only after 300 s clear.
	out = r.feed(70, 400, 10, nil)
	res := only(out, ThermalOvertemp, "RESOLVED")
	if len(res) != 2 || res[0].TsMs != 370_000 {
		t.Fatalf("resolve: %+v", res)
	}
}

func TestRedeliveryGivesSameIDs(t *testing.T) {
	a := newRun(t).feed(0, 60, 10, func(e *event.Event) { e.PackTempMaxC = 60 })
	b := newRun(t).feed(0, 60, 10, func(e *event.Event) { e.PackTempMaxC = 60 })
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("%d vs %d alerts", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatal("replaying the same events must reproduce alert ids")
		}
	}
	if a[0].ID == a[1].ID {
		t.Fatal("warning and critical episodes need distinct ids")
	}
}

func TestLateEventsDoNotDriveRules(t *testing.T) {
	r := newRun(t)
	r.feed(100, 100, 1, nil)
	late := ev(50)
	late.PackTempMaxC = 90
	if out := r.e.Eval(r.v, late, nil, 101_000); len(only(out, ThermalOvertemp, "FIRING")) != 0 || r.v.LateForRules != 1 {
		t.Fatalf("late event evaluated: %+v", out)
	}
}

func TestRiseRate(t *testing.T) {
	r := newRun(t)
	// +1.5 °C/min: no alert.
	out := r.feed(0, 120, 5, func(e *event.Event) { e.PackTempMaxC = 30 + 1.5*float64(e.TsEventMs)/60000 })
	if len(only(out, ThermalRiseRate, "FIRING")) != 0 {
		t.Fatalf("1.5 °C/min fired: %+v", out)
	}
	r = newRun(t)
	out = r.feed(0, 60, 5, func(e *event.Event) { e.PackTempMaxC = 30 + 3*float64(e.TsEventMs)/60000 })
	f := only(out, ThermalRiseRate, "FIRING")
	if len(f) != 1 || f[0].TsMs != 30_000 || f[0].Evidence["value"] < 2.9 {
		t.Fatalf("3 °C/min: %+v", f) // judged once the window spans min_span_s (30 s)
	}
}

func TestDeltaAnomalyFreezesBaseline(t *testing.T) {
	r := newRun(t)
	// 1 h of normal delta (2 ± 0.5 °C) at 5 s, then +12 °C.
	out := r.feed(0, 3600, 5, func(e *event.Event) { e.PackTempMaxC = 32 + float64((e.TsEventMs/5000)%3-1)*0.5 })
	if len(only(out, ThermalDeltaAnomaly, "FIRING")) != 0 {
		t.Fatal("normal data fired")
	}
	base := r.v.delta.Mean
	out = r.feed(3605, 3605+900, 5, func(e *event.Event) { e.PackTempMaxC = 44 })
	f := only(out, ThermalDeltaAnomaly, "FIRING")
	if len(f) != 1 || f[0].TsMs-f[0].WindowStartMs != 600_000 {
		t.Fatalf("anomaly: %+v", f)
	}
	if r.v.delta.Mean != base {
		t.Fatalf("baseline moved while breaching: %.3f → %.3f", base, r.v.delta.Mean)
	}
}

func TestCellImbalanceEscalates(t *testing.T) {
	r := newRun(t)
	out := r.feed(0, 290, 10, func(e *event.Event) { e.CellVMaxMv = e.CellVMinMv + 120 })
	if len(only(out, CellImbalance, "FIRING")) != 0 {
		t.Fatal("fired before 300 s sustained")
	}
	out = r.feed(300, 310, 10, func(e *event.Event) { e.CellVMaxMv = e.CellVMinMv + 120 })
	f := only(out, CellImbalance, "FIRING")
	if len(f) != 2 || f[0].Severity == f[1].Severity {
		t.Fatalf("120 mV for 300 s should fire warning and critical: %+v", f)
	}
}

func TestIsolationOhmPerVolt(t *testing.T) {
	r := newRun(t)
	// 360 V × 500 Ω/V = 180 kΩ: 179 kΩ → warning only; 35 kΩ (97 Ω/V) → critical.
	out := r.feed(0, 20, 10, func(e *event.Event) { e.IsolationKohm = 179 })
	if f := only(out, IsolationLow, "FIRING"); len(f) != 1 || f[0].Severity != "warning" {
		t.Fatalf("179 kΩ: %+v", f)
	}
	out = r.feed(30, 50, 10, func(e *event.Event) { e.IsolationKohm = 35 })
	if f := only(out, IsolationLow, "FIRING"); len(f) != 1 || f[0].Severity != "critical" {
		t.Fatalf("35 kΩ: %+v", f)
	}
	r = newRun(t)
	if out := r.feed(0, 60, 10, func(e *event.Event) { e.IsolationKohm = 181 }); len(only(out, IsolationLow, "FIRING")) != 0 {
		t.Fatal("181 kΩ (>500 Ω/V) must not fire")
	}
}

func TestInterlockOnlyWhileHVActive(t *testing.T) {
	r := newRun(t)
	// One open: no alert. Second open within 5 min: critical.
	out := r.feed(0, 0, 1, func(e *event.Event) { e.HVInterlockOK = false })
	out = append(out, r.feed(60, 60, 1, nil)...)
	if len(only(out, HVInterlockOpen, "FIRING")) != 0 {
		t.Fatal("single open fired")
	}
	out = r.feed(120, 120, 1, func(e *event.Event) { e.HVInterlockOK = false })
	if len(only(out, HVInterlockOpen, "FIRING")) != 1 {
		t.Fatalf("two opens in 5 min: %+v", out)
	}
	// Parked and ignition off: opens are ignored.
	r = newRun(t)
	out = r.feed(0, 100, 10, func(e *event.Event) {
		e.HVInterlockOK, e.SpeedKmh, e.Evt = false, 0, event.EvtIgnOff
	})
	if len(only(out, HVInterlockOpen, "FIRING")) != 0 {
		t.Fatal("interlock open while parked must not alert")
	}
}

func TestAux12vOnlyWhenParked(t *testing.T) {
	parked := func(e *event.Event) { e.Aux12vV, e.SpeedKmh, e.Evt = 11.5, 0, event.EvtIgnOff }
	r := newRun(t)
	out := r.feed(0, 600, 60, parked)
	if f := only(out, Aux12vLow, "FIRING"); len(f) != 1 || f[0].TsMs != 600_000 {
		t.Fatalf("parked 11.5 V for 10 min: %+v", f)
	}
	r = newRun(t)
	if out := r.feed(0, 900, 60, func(e *event.Event) { e.Aux12vV = 11.5 }); len(only(out, Aux12vLow, "FIRING")) != 0 {
		t.Fatal("driving low 12 V must not alert (DC-DC active)")
	}
}

func TestChargeNeededUsesDistanceToDepot(t *testing.T) {
	r := newRun(t)
	veh := &event.Vehicle{VIN: vin, DepotLat: 13.08, DepotLon: 80.27 + 0.4, CapacityKWh: 45, WhPerKm: 190} // ~43 km → ~24 % needed
	var out []Alert
	for s := int64(0); s <= 120; s += 10 {
		e := ev(s)
		e.SoCPct, e.Evt = 30, event.EvtIgnOn
		out = append(out, r.e.Eval(r.v, e, veh, s*1000)...)
	}
	f := only(out, ChargeNeeded, "FIRING")
	if len(f) != 1 || f[0].Severity != "info" || f[0].Evidence["to_depot_pct"] < 20 {
		t.Fatalf("30 %% SoC, ~24 %% needed + 15 %% reserve: %+v", f)
	}
	r = newRun(t)
	veh.DepotLon = 80.27 // at the depot
	out = nil
	for s := int64(0); s <= 120; s += 10 {
		e := ev(s)
		e.SoCPct = 30
		out = append(out, r.e.Eval(r.v, e, veh, s*1000)...)
	}
	if len(only(out, ChargeNeeded, "FIRING")) != 0 {
		t.Fatal("30 % at the depot is above the 15 % reserve")
	}
}

func TestDTCFireOncePerCodeAndResolve(t *testing.T) {
	r := newRun(t)
	out := r.feed(0, 30, 10, func(e *event.Event) { e.DTC = []string{"P0A7E", "U0111"} })
	f := only(out, DTCRaised, "FIRING")
	if len(f) != 2 || f[0].Detail != "P0A7E" || f[0].Severity != "critical" || f[1].Detail != "U0111" || f[1].Severity != "medium" {
		t.Fatalf("fire: %+v", f)
	}
	out = r.feed(40, 110, 10, func(e *event.Event) { e.DTC = []string{"U0111"} })
	res := only(out, DTCRaised, "RESOLVED")
	if len(res) != 1 || res[0].Detail != "P0A7E" || res[0].TsMs != 100_000 {
		t.Fatalf("resolve after 60 s clear: %+v", res)
	}
}

func TestTelemetryStaleSweep(t *testing.T) {
	r := newRun(t)
	r.feed(0, 0, 1, nil) // last event processed at t=0 (processing time)
	if out := r.e.Sweep(r.v, vin, 599_000, true); len(out) != 0 {
		t.Fatal("stale before 600 s")
	}
	out := r.e.Sweep(r.v, vin, 601_000, true)
	if len(out) != 1 || out[0].State != "FIRING" || out[0].RuleID != TelemetryStale {
		t.Fatalf("stale: %+v", out)
	}
	if out := r.e.Sweep(NewVehicle(), vin, 10_000_000, true); out != nil {
		t.Fatal("never-seen vehicle must not be stale")
	}
	back := r.e.Eval(r.v, ev(700), nil, 700_000)
	if res := only(back, TelemetryStale, "RESOLVED"); len(res) != 1 {
		t.Fatalf("next event must resolve stale: %+v", back)
	}
	r2 := newRun(t)
	r2.feed(0, 0, 1, nil)
	if out := r2.e.Sweep(r2.v, vin, 10_000_000, false); len(out) != 0 {
		t.Fatal("off-duty silence is not stale")
	}
}

func TestDisabledAndMissingGroups(t *testing.T) {
	c := loadCfg(t)
	for k, r := range c.Rules {
		r.Enabled = false
		c.Rules[k] = r
	}
	e, v := New(c), NewVehicle()
	x := ev(0)
	x.PackTempMaxC, x.CellVMaxMv, x.IsolationKohm, x.DTC = 99, 9999, 1, []string{"P0A7E"}
	if out := e.Eval(v, x, nil, 0); len(out) != 0 || e.Sweep(v, vin, 1e12, true) != nil {
		t.Fatalf("disabled rules fired: %+v", out)
	}
	r := newRun(t)
	out := r.feed(0, 100, 10, func(e *event.Event) { e.Has, e.PackTempMaxC, e.CellVMaxMv = 0, 99, 9999 })
	if len(only(out, ThermalOvertemp, "FIRING"))+len(only(out, CellImbalance, "FIRING")) != 0 {
		t.Fatal("absent sensor groups must not be evaluated")
	}
	if len(r.v.Firing()) != 0 {
		t.Fatal("nothing should be firing")
	}
}

// A partition that moves mid-episode must not re-fire: the restored vehicle keeps the episode.
func TestSnapshotRestoreKeepsEpisodes(t *testing.T) {
	r := newRun(t)
	out := r.feed(0, 60, 10, func(e *event.Event) { e.PackTempMaxC, e.DTC = 60, []string{"P0A7E"} })
	fired := len(only(out, ThermalOvertemp, "FIRING")) + len(only(out, DTCRaised, "FIRING"))
	if fired != 3 {
		t.Fatalf("setup fired %d", fired)
	}
	snap := r.v.Snapshot()
	moved := &run{r.e, r.e.Restore(snap)}
	out = moved.feed(70, 200, 10, func(e *event.Event) { e.PackTempMaxC, e.DTC = 60, []string{"P0A7E"} })
	if len(only(out, ThermalOvertemp, "FIRING"))+len(only(out, DTCRaised, "FIRING")) != 0 {
		t.Fatalf("restored state re-fired: %+v", out)
	}
	if got := moved.v.Firing(); len(got) != 3 {
		t.Fatalf("firing after restore: %v", got)
	}
	// And it still resolves with the original episode id.
	out = moved.feed(210, 600, 10, nil)
	res := only(out, ThermalOvertemp, "RESOLVED")
	if len(res) != 2 || res[0].WindowStartMs != 0 {
		t.Fatalf("resolve after restore: %+v", res)
	}
	if moved.v.LastTsMs != 600_000 || len(snap.ActiveDTC) != 1 {
		t.Fatalf("snapshot fields: %+v", snap)
	}
}
