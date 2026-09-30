// Package app is the stream-processor use case. A Partition owns the in-memory state of the VINs on
// one Kafka partition and turns events into outputs; the Runner processes a poll batch once, then
// flushes the outputs through the sinks, retrying only the flush (never the processing) until it
// succeeds, and only then lets the caller commit offsets.
package app

import (
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/ist"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/session"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/window"
)

const (
	LatenessMs   = 30_000
	LiveEveryMs  = 1_000   // ≤ 1 live delta per vehicle per second (processing time)
	StateEveryMs = 300_000 // vehicle.state.v1 snapshot cadence per vehicle, plus on any alert change
)

// Registry resolves a VIN to its registry entry (fleet.vehicle.v1).
type Registry interface {
	Get(vin string) (*event.Vehicle, bool)
}

type AlertOut struct {
	rules.Alert
	TenantID, FleetID string
	TriggerIngestMs   int64
	ProcessedMs       int64
}

type SessionOut struct {
	session.Session
	TenantID string
}

// StateOut is the latest-state snapshot for vehicle.state.v1 (and Redis rebuild).
type StateOut struct {
	Latest event.Event
	Firing []string
	Rules  rules.Snapshot
}

// LiveOut is one live-map update (Redis).
type LiveOut struct {
	Latest            event.Event
	TenantID, FleetID string
	Firing            int
}

// Outputs of one poll batch. Raw holds every event (including late ones) for the raw store.
type Outputs struct {
	Raw      []event.Event
	Alerts   []AlertOut
	Sessions []SessionOut
	Rollups  []window.Rollup
	States   []StateOut
	Live     []LiveOut
	DTCs     []DTCHit
	Late     int // beyond lateness for rollups
	LateRule int // older than the vehicle's newest event (not rule-evaluated)
	Unknown  int // VINs not (yet) in the registry
}

// DTCHit feeds the per-tenant top-K sketch.
type DTCHit struct{ TenantID, Code string }

func (o *Outputs) Merge(x Outputs) {
	o.Raw = append(o.Raw, x.Raw...)
	o.Alerts = append(o.Alerts, x.Alerts...)
	o.Sessions = append(o.Sessions, x.Sessions...)
	o.Rollups = append(o.Rollups, x.Rollups...)
	o.States = append(o.States, x.States...)
	o.Live = append(o.Live, x.Live...)
	o.DTCs = append(o.DTCs, x.DTCs...)
	o.Late += x.Late
	o.LateRule += x.LateRule
	o.Unknown += x.Unknown
}

type vstate struct {
	rules       *rules.Vehicle
	sess        session.Tracker
	win         *window.Windows
	latest      event.Event
	liveAtMs    int64
	stateAtMs   int64
	firingCount int
}

// Partition holds the state for the VINs of one Kafka partition. Not safe for concurrent use; the
// Runner gives each partition its own goroutine.
type Partition struct {
	ID       int32
	engine   *rules.Engine
	registry Registry
	vins     map[string]*vstate
}

func NewPartition(id int32, e *rules.Engine, reg Registry) *Partition {
	return &Partition{ID: id, engine: e, registry: reg, vins: map[string]*vstate{}}
}

func (p *Partition) get(vin string) *vstate {
	v, ok := p.vins[vin]
	if !ok {
		v = &vstate{rules: rules.NewVehicle(), win: window.New(LatenessMs)}
		p.vins[vin] = v
	}
	return v
}

// Restore seeds rule state from vehicle.state.v1 snapshots when the partition is assigned.
func (p *Partition) Restore(snaps []StateOut) {
	for _, s := range snaps {
		v := p.get(s.Latest.VIN)
		v.rules = p.engine.Restore(s.Rules)
		v.latest = s.Latest
		v.firingCount = len(s.Firing)
	}
}

func (p *Partition) alerts(o *Outputs, as []rules.Alert, veh *event.Vehicle, ingestMs, nowMs int64) {
	for _, a := range as {
		out := AlertOut{Alert: a, TriggerIngestMs: ingestMs, ProcessedMs: nowMs}
		if veh != nil {
			out.TenantID, out.FleetID = veh.TenantID, veh.FleetID
		}
		o.Alerts = append(o.Alerts, out)
	}
}

// Process consumes events in partition order at processing time nowMs.
func (p *Partition) Process(evs []event.Event, nowMs int64) Outputs {
	var o Outputs
	o.Raw = evs
	touched := map[string]bool{}
	for i := range evs {
		ev := &evs[i]
		v := p.get(ev.VIN)
		veh, known := p.registry.Get(ev.VIN)
		if !known {
			o.Unknown++
			veh = nil
		}
		if !v.win.Add(ev) {
			o.Late++
		}
		lateBefore := v.rules.LateForRules
		as := p.engine.Eval(v.rules, ev, veh, nowMs)
		if v.rules.LateForRules > lateBefore {
			o.LateRule++
			continue // older than this vehicle's newest event: stored and rolled up, nothing else
		}
		p.alerts(&o, as, veh, ev.TsIngestMs, nowMs)
		capAh := 0.0
		if veh != nil {
			if veh.NominalV > 0 {
				capAh = veh.CapacityKWh * 1000 / veh.NominalV
			}
			for _, a := range as {
				if a.RuleID == rules.DTCRaised && a.State == "FIRING" {
					o.DTCs = append(o.DTCs, DTCHit{veh.TenantID, a.Detail})
				}
			}
		}
		for _, s := range v.sess.Step(ev, capAh) {
			so := SessionOut{Session: s}
			if veh != nil {
				so.TenantID = veh.TenantID
			}
			o.Sessions = append(o.Sessions, so)
		}
		v.latest = *ev
		firing := len(v.rules.Firing())
		if len(as) > 0 || firing != v.firingCount || nowMs-v.stateAtMs >= StateEveryMs {
			v.stateAtMs, v.firingCount = nowMs, firing
			o.States = append(o.States, StateOut{Latest: *ev, Firing: v.rules.Firing(), Rules: v.rules.Snapshot()})
		}
		if nowMs-v.liveAtMs >= LiveEveryMs {
			v.liveAtMs = nowMs
			lo := LiveOut{Latest: *ev, Firing: firing}
			if veh != nil {
				lo.TenantID, lo.FleetID = veh.TenantID, veh.FleetID
			}
			o.Live = append(o.Live, lo)
		}
		touched[ev.VIN] = true
	}
	for vin := range touched {
		o.Rollups = append(o.Rollups, p.vins[vin].win.Close()...)
	}
	return o
}

// Sweep runs the processing-time TELEMETRY_STALE check for every vehicle of this partition.
func (p *Partition) Sweep(now time.Time) Outputs {
	var o Outputs
	nowMs, minIST := now.UnixMilli(), ist.MinuteOfDay(now)
	for vin, v := range p.vins {
		veh, ok := p.registry.Get(vin)
		onDuty := ok && ist.InShift(minIST, veh.DepartMinIST, veh.ReturnMinIST)
		if !ok {
			veh = nil
		}
		p.alerts(&o, p.engine.Sweep(v.rules, vin, nowMs, onDuty), veh, 0, nowMs)
	}
	return o
}

// Flush closes every open rollup window (partition revoke, shutdown).
func (p *Partition) Flush() Outputs {
	var o Outputs
	for _, v := range p.vins {
		o.Rollups = append(o.Rollups, v.win.Flush()...)
	}
	return o
}

// Vehicles is the number of VINs held (metrics).
func (p *Partition) Vehicles() int { return len(p.vins) }
