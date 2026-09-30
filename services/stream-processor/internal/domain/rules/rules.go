// Package rules evaluates the real-time battery safety rules (services/stream-processor/CLAUDE.md)
// for one vehicle at a time. Thresholds come from config/rules.yaml via Config; nothing is
// hard-coded here. Each rule turns an event into an instantaneous breach/clear observation;
// alert.Machine adds sustain (For) and hysteresis (ClearFor).
//
// Rules run on per-VIN monotonic event time: an event older than the vehicle's newest processed
// event is not evaluated (it is still stored and still counted in rollups). This keeps alert
// latency independent of the 30 s lateness allowance. Eval is O(window samples) per event.
package rules

import (
	"math"
	"sort"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/alert"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/ewma"
)

// Rule IDs (the alert contract).
const (
	ThermalOvertemp     = "THERMAL_OVERTEMP"
	ThermalRiseRate     = "THERMAL_RISE_RATE"
	ThermalDeltaAnomaly = "THERMAL_DELTA_ANOMALY"
	CellImbalance       = "CELL_IMBALANCE"
	IsolationLow        = "ISOLATION_LOW"
	HVInterlockOpen     = "HV_INTERLOCK_OPEN"
	Aux12vLow           = "AUX_12V_LOW"
	ChargeNeeded        = "CHARGE_NEEDED"
	DTCRaised           = "DTC_RAISED"
	TelemetryStale      = "TELEMETRY_STALE"
)

type Level struct {
	Severity  string  `yaml:"severity"`
	Threshold float64 `yaml:"threshold"`
}

// Rule is one rule's configuration. Fields a rule does not use are ignored.
type Rule struct {
	Enabled    bool    `yaml:"enabled"`
	Levels     []Level `yaml:"levels"`      // most to least severe is not required; each level is its own machine
	ForS       int64   `yaml:"for_s"`       // breach must hold this long
	ClearForS  int64   `yaml:"clear_for_s"` // clear must hold this long to resolve
	WindowS    int64   `yaml:"window_s"`    // rise-rate slope window / interlock counting window
	MinSpanS   int64   `yaml:"min_span_s"`  // rise rate: minimum window coverage before judging
	Count      int     `yaml:"count"`       // interlock: opens within window
	Z          float64 `yaml:"z"`           // anomaly: z-score threshold
	Alpha      float64 `yaml:"alpha"`       // anomaly: EWMA weight per sample
	MinN       int     `yaml:"min_n"`       // anomaly: samples before judging
	ReservePct float64 `yaml:"reserve_pct"` // charge needed
	StaleS     int64   `yaml:"stale_s"`     // telemetry stale (processing time)
}

// Config maps rule ID → rule; DTCSeverity maps code → severity from data/reference/dtc_catalogue.csv.
type Config struct {
	Rules       map[string]Rule `yaml:"rules"`
	DTCSeverity map[string]string
}

// Alert is an alert state change.
type Alert struct {
	ID, VIN, RuleID, Severity string
	State                     string // FIRING | RESOLVED
	WindowStartMs, TsMs       int64
	Evidence                  map[string]float64
	Detail                    string // e.g. the DTC code
}

type tempSample struct {
	ts int64
	t  float64
}

// Vehicle is the rule state for one VIN (lives in memory per Kafka partition).
type Vehicle struct {
	LastTsMs     int64
	LastProcMs   int64
	seen         bool // an event has been processed (LastProcMs is valid)
	machines     map[string]*alert.Machine
	temps        []tempSample
	delta        ewma.EWMA
	opens        []int64
	ignOn        bool
	activeDTC    map[string]bool
	LateForRules int
}

func NewVehicle() *Vehicle {
	return &Vehicle{machines: map[string]*alert.Machine{}, activeDTC: map[string]bool{}}
}

type Engine struct{ cfg Config }

func New(cfg Config) *Engine { return &Engine{cfg: cfg} }

func (e *Engine) machine(v *Vehicle, key string, r Rule) *alert.Machine {
	m, ok := v.machines[key]
	if !ok {
		m = &alert.Machine{ForMs: r.ForS * 1000, ClearForM: r.ClearForS * 1000}
		v.machines[key] = m
	}
	return m
}

// observe steps one machine and appends the resulting alert, if any.
func (e *Engine) observe(out []Alert, v *Vehicle, vin, rule, sev, detail string, r Rule, ts int64, breach bool, ev map[string]float64) []Alert {
	key := rule + "|" + sev + "|" + detail
	m := e.machine(v, key, r)
	tr := m.Step(ts, breach)
	if tr == alert.None {
		return out
	}
	idRule := rule + ":" + sev
	if detail != "" {
		idRule += ":" + detail
	}
	a := Alert{ID: alert.ID(vin, idRule, m.StartMs), VIN: vin, RuleID: rule, Severity: sev, WindowStartMs: m.StartMs, TsMs: ts, Evidence: ev, Detail: detail}
	if tr == alert.Fired {
		a.State = "FIRING"
	} else {
		a.State = "RESOLVED"
	}
	return append(out, a)
}

func (e *Engine) levels(out []Alert, v *Vehicle, vin, rule string, ts int64, value float64, below bool, ev map[string]float64) []Alert {
	r, ok := e.cfg.Rules[rule]
	if !ok || !r.Enabled {
		return out
	}
	for _, l := range r.Levels {
		breach := value >= l.Threshold
		if below {
			breach = value < l.Threshold
		}
		evd := map[string]float64{"value": value, "threshold": l.Threshold}
		for k, x := range ev {
			evd[k] = x
		}
		out = e.observe(out, v, vin, rule, l.Severity, "", r, ts, breach, evd)
	}
	return out
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// Eval runs every event-driven rule for one event. veh may be nil (unregistered VIN): rules that
// need registry data are then skipped.
func (e *Engine) Eval(v *Vehicle, ev *event.Event, veh *event.Vehicle, procNowMs int64) []Alert {
	v.LastProcMs, v.seen = procNowMs, true
	var out []Alert
	// Any event ends a stale episode (processing time).
	if r, ok := e.cfg.Rules[TelemetryStale]; ok && r.Enabled {
		out = e.observe(out, v, ev.VIN, TelemetryStale, "warning", "", r, procNowMs, false, nil)
	}
	if ev.TsEventMs < v.LastTsMs {
		v.LateForRules++
		return out
	}
	v.LastTsMs = ev.TsEventMs
	ts := ev.TsEventMs
	switch ev.Evt {
	case event.EvtIgnOn:
		v.ignOn = true
	case event.EvtIgnOff:
		v.ignOn = false
	}
	hvActive := v.ignOn || ev.ChargePowerKW > 0 || ev.SpeedKmh > 0

	if ev.Has&event.HasTemp != 0 {
		out = e.levels(out, v, ev.VIN, ThermalOvertemp, ts, ev.PackTempMaxC, false, nil)
		out = e.riseRate(out, v, ev)
		out = e.deltaAnomaly(out, v, ev)
	}
	if ev.Has&event.HasCells != 0 {
		out = e.levels(out, v, ev.VIN, CellImbalance, ts, ev.Imbalance(), false, nil)
	}
	if ev.PackVoltageV > 1 {
		ohmPerVolt := ev.IsolationKohm * 1000 / ev.PackVoltageV
		out = e.levels(out, v, ev.VIN, IsolationLow, ts, ohmPerVolt, true, map[string]float64{"isolation_kohm": ev.IsolationKohm, "pack_voltage_v": ev.PackVoltageV})
	}
	out = e.interlock(out, v, ev, hvActive)
	if parked := !hvActive; parked {
		out = e.levels(out, v, ev.VIN, Aux12vLow, ts, ev.Aux12vV, true, nil)
	}
	if veh != nil && ev.Has&event.HasGPS != 0 && veh.CapacityKWh > 0 {
		out = e.chargeNeeded(out, v, ev, veh, hvActive)
	}
	out = e.dtcs(out, v, ev)
	return out
}

func (e *Engine) riseRate(out []Alert, v *Vehicle, ev *event.Event) []Alert {
	r, ok := e.cfg.Rules[ThermalRiseRate]
	if !ok || !r.Enabled || len(r.Levels) == 0 {
		return out
	}
	v.temps = append(v.temps, tempSample{ev.TsEventMs, ev.PackTempMaxC})
	cut := ev.TsEventMs - r.WindowS*1000
	i := 0
	for i < len(v.temps) && v.temps[i].ts < cut {
		i++
	}
	v.temps = v.temps[i:]
	first := v.temps[0]
	span := ev.TsEventMs - first.ts
	if span < r.MinSpanS*1000 {
		return out
	}
	perMin := (ev.PackTempMaxC - first.t) / (float64(span) / 60000)
	return e.levels(out, v, ev.VIN, ThermalRiseRate, ev.TsEventMs, perMin, false, map[string]float64{"window_s": float64(span) / 1000})
}

// deltaAnomaly compares (pack max − ambient) with the vehicle's own EWMA baseline. The baseline is
// frozen while breaching, so a slow fault cannot teach the baseline that "hot is normal".
func (e *Engine) deltaAnomaly(out []Alert, v *Vehicle, ev *event.Event) []Alert {
	r, ok := e.cfg.Rules[ThermalDeltaAnomaly]
	if !ok || !r.Enabled {
		return out
	}
	x := ev.PackTempMaxC - ev.AmbientC
	if v.delta.Alpha == 0 {
		v.delta.Alpha = r.Alpha
	}
	z := v.delta.Z(x, r.MinN)
	breach := z > r.Z
	if !breach {
		v.delta.Update(x)
	}
	sev := "warning"
	if len(r.Levels) > 0 {
		sev = r.Levels[0].Severity
	}
	return e.observe(out, v, ev.VIN, ThermalDeltaAnomaly, sev, "", r, ev.TsEventMs, breach,
		map[string]float64{"delta_c": x, "baseline_c": v.delta.Mean, "z": z, "threshold_z": r.Z})
}

func (e *Engine) interlock(out []Alert, v *Vehicle, ev *event.Event, hvActive bool) []Alert {
	r, ok := e.cfg.Rules[HVInterlockOpen]
	if !ok || !r.Enabled {
		return out
	}
	if !ev.HVInterlockOK && hvActive {
		v.opens = append(v.opens, ev.TsEventMs)
	}
	cut := ev.TsEventMs - r.WindowS*1000
	i := 0
	for i < len(v.opens) && v.opens[i] < cut {
		i++
	}
	v.opens = v.opens[i:]
	sev := "critical"
	if len(r.Levels) > 0 {
		sev = r.Levels[0].Severity
	}
	return e.observe(out, v, ev.VIN, HVInterlockOpen, sev, "", r, ev.TsEventMs, len(v.opens) >= r.Count,
		map[string]float64{"opens_in_window": float64(len(v.opens)), "window_s": float64(r.WindowS)})
}

// chargeNeeded: SoC below reserve + the energy needed to drive back to the depot.
func (e *Engine) chargeNeeded(out []Alert, v *Vehicle, ev *event.Event, veh *event.Vehicle, driving bool) []Alert {
	r, ok := e.cfg.Rules[ChargeNeeded]
	if !ok || !r.Enabled {
		return out
	}
	km := haversineKm(ev.Lat, ev.Lon, veh.DepotLat, veh.DepotLon) * 1.3 // road factor over straight line
	needPct := km * veh.WhPerKm / 1000 / veh.CapacityKWh * 100
	breach := driving && ev.SoCPct < r.ReservePct+needPct
	sev := "info"
	if len(r.Levels) > 0 {
		sev = r.Levels[0].Severity
	}
	return e.observe(out, v, ev.VIN, ChargeNeeded, sev, "", r, ev.TsEventMs, breach,
		map[string]float64{"soc_pct": ev.SoCPct, "reserve_pct": r.ReservePct, "to_depot_km": km, "to_depot_pct": needPct})
}

// dtcs fires once per newly present code and resolves when the code disappears.
func (e *Engine) dtcs(out []Alert, v *Vehicle, ev *event.Event) []Alert {
	r, ok := e.cfg.Rules[DTCRaised]
	if !ok || !r.Enabled {
		return out
	}
	now := map[string]bool{}
	for _, c := range ev.DTC {
		now[c] = true
	}
	codes := make([]string, 0, len(now)+len(v.activeDTC))
	for c := range now {
		codes = append(codes, c)
	}
	for c := range v.activeDTC {
		if !now[c] {
			codes = append(codes, c)
		}
	}
	sort.Strings(codes) // deterministic output order
	for _, c := range codes {
		sev := e.cfg.DTCSeverity[c]
		if sev == "" {
			sev = "warning"
		}
		out = e.observe(out, v, ev.VIN, DTCRaised, sev, c, r, ev.TsEventMs, now[c], map[string]float64{"dtc_count": float64(len(ev.DTC))})
		if now[c] {
			v.activeDTC[c] = true
		} else if m := v.machines[DTCRaised+"|"+sev+"|"+c]; m == nil || m.Phase == alert.OK {
			delete(v.activeDTC, c)
		}
	}
	return out
}

// Sweep is the processing-time check for TELEMETRY_STALE: silent for > stale_s while on duty.
func (e *Engine) Sweep(v *Vehicle, vin string, procNowMs int64, onDuty bool) []Alert {
	r, ok := e.cfg.Rules[TelemetryStale]
	if !ok || !r.Enabled || !v.seen {
		return nil
	}
	silentMs := procNowMs - v.LastProcMs
	breach := onDuty && silentMs > r.StaleS*1000
	return e.observe(nil, v, vin, TelemetryStale, "warning", "", r, procNowMs, breach,
		map[string]float64{"silent_s": float64(silentMs) / 1000, "stale_s": float64(r.StaleS)})
}

// Firing lists the rule keys currently firing (for state snapshots and tests).
func (v *Vehicle) Firing() []string {
	var out []string
	for k, m := range v.machines {
		if m.Phase == alert.Firing {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
