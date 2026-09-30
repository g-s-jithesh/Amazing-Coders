// Package fault is the catalogue of injectable battery faults (CLAUDE.md §6). Each fault has a
// precursor window during which a telemetry signal drifts (progress p: 0 → 1); the DTC is raised
// when p reaches 1, so every DTC is predictable from earlier telemetry. Pure and O(1).
package fault

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type Kind uint8

const (
	None Kind = iota
	CoolingDegradation
	CellDrift
	InsulationWear
	ConnectorIssue
	WeakAux
)

type Spec struct {
	Kind                       Kind
	Name                       string
	DTC                        []string // must exist in data/reference/dtc_catalogue.csv
	PrecursorMin, PrecursorMax time.Duration
	Weight                     float64 // relative frequency
	AgeSensitive               bool    // hazard scales with pack fade
}

// Catalogue, in Kind order (index = Kind).
var Catalogue = []Spec{
	{},
	{CoolingDegradation, "cooling_degradation", []string{"P0A7E"}, 48 * time.Hour, 120 * time.Hour, 0.25, false},
	{CellDrift, "cell_drift", []string{"P0A7F"}, 72 * time.Hour, 120 * time.Hour, 0.25, true},
	{InsulationWear, "insulation_wear", []string{"P0AA6"}, 48 * time.Hour, 96 * time.Hour, 0.15, true},
	{ConnectorIssue, "connector_issue", []string{"P0A0A"}, 24 * time.Hour, 72 * time.Hour, 0.15, false},
	{WeakAux, "weak_aux_battery", []string{"P0562", "U0111"}, 24 * time.Hour, 72 * time.Hour, 0.20, false},
}

// RepairAfter is how long a raised DTC stays active before the depot fixes the vehicle.
const RepairAfter = 72 * time.Hour

func (k Kind) String() string {
	if int(k) < len(Catalogue) && k != None {
		return Catalogue[k].Name
	}
	return "none"
}

// Parse maps a catalogue name to its Kind.
func Parse(name string) (Kind, error) {
	for _, s := range Catalogue[1:] {
		if s.Name == name {
			return s.Kind, nil
		}
	}
	return None, fmt.Errorf("fault: unknown kind %q", name)
}

// ParseInject parses a demo-inject spec "<VIN>:<fault>".
func ParseInject(spec string) (vin string, k Kind, err error) {
	vin, name, ok := strings.Cut(spec, ":")
	if !ok || vin == "" {
		return "", None, fmt.Errorf("fault: inject spec %q, want <vin>:<fault>", spec)
	}
	k, err = Parse(name)
	return vin, k, err
}

// Progress is the precursor progress at now, clamped to [0, 1].
func Progress(startMs, dtcMs, nowMs int64) float64 {
	if dtcMs <= startMs {
		return 1
	}
	return math.Max(0, math.Min(1, float64(nowMs-startMs)/float64(dtcMs-startMs)))
}

// Effects are what a fault does to the vehicle at progress p. Zero value = healthy.
type Effects struct {
	CoolingEff        float64 // multiplier on active cooling (1 healthy)
	ExtraHeatRiseC    float64 // steady-state pack temperature rise above ambient from the fault
	ExtraImbalanceMv  float64
	IsolationFactor   float64 // multiplier on isolation resistance (1 healthy)
	InterlockFlapPerH float64 // expected HV-interlock open events per hour
	AuxSagV           float64 // 12 V drop while the vehicle sleeps
}

// Healthy is the no-fault effect set.
var Healthy = Effects{CoolingEff: 1, IsolationFactor: 1}

// EffectsAt returns the effects of kind k at progress p. The signal curves are the learnable
// precursors: temperature delta, cell imbalance, isolation, interlock flaps and 12 V level.
func EffectsAt(k Kind, p float64) Effects {
	e := Healthy
	switch k {
	case CoolingDegradation:
		e.CoolingEff = (1 - p) * (1 - p) // pump/loop failing; fully failed at the DTC
		e.ExtraHeatRiseC = 25 * p        // ≈ +25 °C over ambient when the DTC fires
	case CellDrift:
		e.ExtraImbalanceMv = 150 * p * p
	case InsulationWear:
		e.IsolationFactor = math.Pow(0.04, p) // falls ~25× (e.g. 2500 kΩ → 100 kΩ)
	case ConnectorIssue:
		e.InterlockFlapPerH = 0.2 + 6*p*p
	case WeakAux:
		e.AuxSagV = 1.4 * p // 12.6 V → 11.2 V parked
	}
	return e
}
