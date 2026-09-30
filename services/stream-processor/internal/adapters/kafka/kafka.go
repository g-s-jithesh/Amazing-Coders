// Package kafka connects the stream-processor to Kafka (franz-go): the telemetry consumer (group
// "stream-processor", cooperative-sticky, manual commits after sinks succeed), the output emitter,
// the fleet.vehicle.v1 registry and the vehicle.state.v1 snapshot loader used on partition assign.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"

	alertsv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/alerts/v1"
	fleetv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/fleet/v1"
	sessionsv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/sessions/v1"
	telemetryv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/telemetry/v1"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/session"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/window"
)

const (
	TopicCanonical = "telemetry.canonical.v1"
	TopicAlerts    = "alerts.v1"
	TopicSessions  = "battery.sessions.v1"
	TopicRollups   = "telemetry.rollup.1m.v1"
	TopicState     = "vehicle.state.v1"
	TopicRegistry  = "fleet.vehicle.v1"
	Group          = "stream-processor"
)

// ---- mapping ----

func FromProto(m *telemetryv1.TelemetryEvent) event.Event {
	e := event.Event{
		VIN: m.Vin, TsEventMs: m.TsEventMs, TsIngestMs: m.TsIngestMs, Seq: m.Seq, OdoKm: m.OdoKm,
		SoCPct: float64(m.SocPct), PackVoltageV: float64(m.PackVoltageV), PackCurrentA: float64(m.PackCurrentA),
		IsolationKohm: float64(m.IsolationKohm), HVInterlockOK: m.HvInterlockOk, Aux12vV: float64(m.Aux_12VV),
		AmbientC: float64(m.AmbientC), ChargeState: int32(m.ChargeState), ChargePowerKW: float64(m.ChargePowerKw),
		DTC: m.Dtc, Evt: int32(m.Evt),
	}
	if m.Lat != nil && m.Lon != nil && m.SpeedKmh != nil {
		e.Lat, e.Lon, e.SpeedKmh, e.Has = *m.Lat, *m.Lon, float64(*m.SpeedKmh), e.Has|event.HasGPS
	}
	if m.PackTempMinC != nil && m.PackTempMaxC != nil {
		e.PackTempMinC, e.PackTempMaxC, e.Has = float64(*m.PackTempMinC), float64(*m.PackTempMaxC), e.Has|event.HasTemp
	}
	if m.CellVMinMv != nil && m.CellVMaxMv != nil {
		e.CellVMinMv, e.CellVMaxMv, e.Has = float64(*m.CellVMinMv), float64(*m.CellVMaxMv), e.Has|event.HasCells
	}
	return e
}

var severity = map[string]alertsv1.Severity{
	"info": alertsv1.Severity_SEVERITY_INFO, "warning": alertsv1.Severity_SEVERITY_WARNING, "medium": alertsv1.Severity_SEVERITY_WARNING,
	"high": alertsv1.Severity_SEVERITY_HIGH, "critical": alertsv1.Severity_SEVERITY_CRITICAL,
}

func alertProto(a app.AlertOut) *alertsv1.Alert {
	st := alertsv1.AlertState_ALERT_STATE_FIRING
	if a.State == "RESOLVED" {
		st = alertsv1.AlertState_ALERT_STATE_RESOLVED
	}
	return &alertsv1.Alert{AlertId: a.ID, Vin: a.VIN, TenantId: a.TenantID, FleetId: a.FleetID, RuleId: a.RuleID,
		Severity: severity[a.Severity], State: st, WindowStartMs: a.WindowStartMs, TsEventMs: a.TsMs,
		TriggerIngestMs: a.TriggerIngestMs, ProcessedMs: a.ProcessedMs, Evidence: a.Evidence, Detail: a.Detail}
}

func sessionProto(s app.SessionOut) *sessionsv1.Session {
	k, ph := sessionsv1.Kind_KIND_CHARGE, sessionsv1.Phase_PHASE_START
	if s.Kind == session.Drive {
		k = sessionsv1.Kind_KIND_DRIVE
	}
	if s.Phase == "END" {
		ph = sessionsv1.Phase_PHASE_END
	}
	m := &sessionsv1.Session{SessionId: s.ID, Vin: s.VIN, TenantId: s.TenantID, Kind: k, Phase: ph, StartMs: s.StartMs, EndMs: s.EndMs,
		SocStartPct: s.SoCStart, SocEndPct: s.SoCEnd, DeltaAh: s.DeltaAh, DeltaKwh: s.DeltaKWh, TempMinC: s.TempMinC, TempMaxC: s.TempMaxC,
		MaxCRate: s.MaxCRate, ChargerType: s.ChargerType, DistanceKm: s.DistanceKm, Gaps: int32(s.Gaps), Samples: int32(s.Samples)}
	if s.HasRestBefore {
		m.VRestBefore = proto.Float64(s.VRestBefore)
	}
	return m
}

func stateProto(s app.StateOut) *telemetryv1.VehicleState {
	r := &telemetryv1.RuleSnapshot{LastTsMs: s.Rules.LastTsMs, IgnOn: s.Rules.IgnOn, DeltaMean: s.Rules.DeltaMean,
		DeltaVar: s.Rules.DeltaVar, DeltaN: int32(s.Rules.DeltaN), ActiveDtc: s.Rules.ActiveDTC}
	for _, m := range s.Rules.Machines {
		r.Machines = append(r.Machines, &telemetryv1.MachineState{Key: m.Key, Phase: int32(m.Phase), StartMs: m.StartMs, ClearMs: m.ClearMs, LastMs: m.LastMs})
	}
	l := s.Latest
	return &telemetryv1.VehicleState{Vin: l.VIN, TsEventMs: l.TsEventMs, Lat: l.Lat, Lon: l.Lon, SpeedKmh: l.SpeedKmh, SocPct: l.SoCPct,
		PackTempMaxC: l.PackTempMaxC, ChargeState: telemetryv1.ChargeState(l.ChargeState), Firing: s.Firing, Rules: r}
}

// StateFromProto reverses stateProto (partition assign).
func StateFromProto(m *telemetryv1.VehicleState) app.StateOut {
	s := app.StateOut{Firing: m.Firing, Latest: event.Event{VIN: m.Vin, TsEventMs: m.TsEventMs, Lat: m.Lat, Lon: m.Lon,
		SpeedKmh: m.SpeedKmh, SoCPct: m.SocPct, PackTempMaxC: m.PackTempMaxC, ChargeState: int32(m.ChargeState)}}
	if r := m.Rules; r != nil {
		s.Rules = rules.Snapshot{LastTsMs: r.LastTsMs, IgnOn: r.IgnOn, DeltaMean: r.DeltaMean, DeltaVar: r.DeltaVar, DeltaN: int(r.DeltaN), ActiveDTC: r.ActiveDtc}
		for _, x := range r.Machines {
			s.Rules.Machines = append(s.Rules.Machines, rules.MachineSnap{Key: x.Key, Phase: uint8(x.Phase), StartMs: x.StartMs, ClearMs: x.ClearMs, LastMs: x.LastMs})
		}
	}
	return s
}

func rollupProto(x window.Rollup) *telemetryv1.Rollup1M {
	m := &telemetryv1.Rollup1M{Vin: x.VIN, MinuteStartMs: x.MinuteStartMs, Count: int32(x.Count), SocMinPct: x.SoCMin, SocMaxPct: x.SoCMax,
		SocAvgPct: x.SoCAvg, SpeedAvgKmh: x.SpeedAvgKmh, DistanceKm: x.DistanceKm, EnergyKwh: x.EnergyKWh, ChargeKwhGrid: x.ChargeKWhGrid,
		IsolationMinKohm: x.IsolationMinKohm}
	if x.HasTemp {
		m.TempMaxC = proto.Float64(x.TempMaxC)
	}
	if x.HasCells {
		m.ImbalanceMaxMv = proto.Float64(x.ImbalanceMaxMv)
	}
	return m
}

// ---- emitter ----

type Emitter struct{ cl *kgo.Client }

func NewEmitter(brokers []string) (*Emitter, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(2*time.Millisecond), kgo.ProducerBatchCompression(kgo.ZstdCompression()), kgo.RecordDeliveryTimeout(30*time.Second))
	if err != nil {
		return nil, err
	}
	return &Emitter{cl: cl}, nil
}

// Emit produces every output record and waits for all acknowledgements.
func (e *Emitter) Emit(ctx context.Context, o app.Outputs) error {
	var recs []*kgo.Record
	add := func(topic, key string, m proto.Message) error {
		b, err := proto.Marshal(m)
		if err != nil {
			return err
		}
		recs = append(recs, &kgo.Record{Topic: topic, Key: []byte(key), Value: b})
		return nil
	}
	for _, a := range o.Alerts { // alerts first: they are the latency-critical output
		if err := add(TopicAlerts, a.VIN, alertProto(a)); err != nil {
			return err
		}
	}
	for _, s := range o.Sessions {
		if err := add(TopicSessions, s.VIN, sessionProto(s)); err != nil {
			return err
		}
	}
	for _, x := range o.Rollups {
		if err := add(TopicRollups, x.VIN, rollupProto(x)); err != nil {
			return err
		}
	}
	for _, s := range o.States {
		if err := add(TopicState, s.Latest.VIN, stateProto(s)); err != nil {
			return err
		}
	}
	if len(recs) == 0 {
		return nil
	}
	return e.cl.ProduceSync(ctx, recs...).FirstErr()
}

func (e *Emitter) Close() { e.cl.Close() }

// ---- registry (ADR-0006) ----

type Registry struct {
	mu        sync.RWMutex
	m         map[string]*event.Vehicle
	caughtUp  atomic.Bool
	decodeErr atomic.Int64
}

func (r *Registry) Get(vin string) (*event.Vehicle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.m[vin]
	return v, ok
}

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.m)
}

func (r *Registry) CaughtUp() bool { return r.caughtUp.Load() }

func (r *Registry) apply(rec *kgo.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec.Value == nil { // tombstone: vehicle removed
		delete(r.m, string(rec.Key))
		return
	}
	var v fleetv1.Vehicle
	if err := proto.Unmarshal(rec.Value, &v); err != nil {
		r.decodeErr.Add(1)
		return
	}
	r.m[v.Vin] = &event.Vehicle{VIN: v.Vin, TenantID: v.TenantId, FleetID: v.FleetId, DepotID: v.DepotId, DepotLat: v.DepotLat,
		DepotLon: v.DepotLon, CapacityKWh: v.CapacityKwh, WhPerKm: v.WhPerKm, NominalV: v.NominalVoltageV,
		DepartMinIST: int(v.DepartMinIst), ReturnMinIST: int(v.ReturnMinIst)}
}

// endOffsets asks the broker for each partition's current end (high watermark), so "caught up"
// is known even for empty partitions, which return no fetch data at all.
func endOffsets(ctx context.Context, cl *kgo.Client, topic string) (map[int32]int64, error) {
	ends, err := kadm.NewClient(cl).ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, err
	}
	out := map[int32]int64{}
	var firstErr error
	ends.Each(func(o kadm.ListedOffset) {
		if o.Err != nil && firstErr == nil {
			firstErr = o.Err
		}
		out[o.Partition] = o.Offset
	})
	return out, firstErr
}

func reached(pos, target map[int32]int64) bool {
	for p, end := range target {
		if pos[p] < end {
			return false
		}
	}
	return true
}

// FollowRegistry reads fleet.vehicle.v1 from the beginning and keeps following it. CaughtUp turns
// true once every partition reached the end offset it had when we started.
func FollowRegistry(ctx context.Context, brokers []string) (*Registry, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(TopicRegistry),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.FetchMaxWait(500*time.Millisecond))
	if err != nil {
		return nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	target, err := endOffsets(lctx, cl, TopicRegistry)
	cancel()
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("registry end offsets: %w", err)
	}
	r := &Registry{m: map[string]*event.Vehicle{}}
	pos := map[int32]int64{}
	r.caughtUp.Store(reached(pos, target))
	go func() {
		defer cl.Close()
		for ctx.Err() == nil {
			cl.PollFetches(ctx).EachPartition(func(p kgo.FetchTopicPartition) {
				for _, rec := range p.Records {
					r.apply(rec)
					pos[p.Partition] = rec.Offset + 1
				}
			})
			if !r.caughtUp.Load() && reached(pos, target) {
				r.caughtUp.Store(true)
			}
		}
	}()
	return r, nil
}

// ---- state loader (partition assign) ----

// LoadStates reads one partition of vehicle.state.v1 up to its current end and returns the latest
// snapshot per VIN. canonical and state topics have the same partition count and the same key
// (VIN), so partition p of one holds exactly the VINs of partition p of the other.
func LoadStates(ctx context.Context, brokers []string, partition int32) ([]app.StateOut, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.FetchMaxWait(300*time.Millisecond),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{TopicState: {partition: kgo.NewOffset().AtStart()}}))
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ends, err := endOffsets(ctx, cl, TopicState)
	if err != nil {
		return nil, fmt.Errorf("state end offsets: %w", err)
	}
	target := map[int32]int64{partition: ends[partition]}
	pos := map[int32]int64{}
	latest := map[string]*telemetryv1.VehicleState{}
	for !reached(pos, target) {
		fs := cl.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("load %s[%d]: %w", TopicState, partition, err)
		}
		fs.EachRecord(func(rec *kgo.Record) {
			var s telemetryv1.VehicleState
			if proto.Unmarshal(rec.Value, &s) == nil {
				latest[s.Vin] = &s
			}
			pos[rec.Partition] = rec.Offset + 1
		})
	}
	out := make([]app.StateOut, 0, len(latest))
	for _, s := range latest {
		out = append(out, StateFromProto(s))
	}
	return out, nil
}

// ---- consumer ----

type Metrics interface {
	DecodeError()
	Commit(time.Duration)
}

type Consumer struct {
	cl      *kgo.Client
	runner  *app.Runner
	brokers []string
	m       Metrics
}

func NewConsumer(brokers []string, runner *app.Runner, m Metrics) (*Consumer, error) {
	c := &Consumer{runner: runner, brokers: brokers, m: m}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(Group),
		kgo.ConsumeTopics(TopicCanonical),
		kgo.Balancers(kgo.CooperativeStickyBalancer()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(200*time.Millisecond),
		kgo.OnPartitionsAssigned(c.assigned),
		kgo.OnPartitionsRevoked(c.revoked),
		kgo.OnPartitionsLost(c.revoked),
	)
	if err != nil {
		return nil, err
	}
	c.cl = cl
	return c, nil
}

func (c *Consumer) assigned(ctx context.Context, _ *kgo.Client, m map[string][]int32) {
	for _, p := range m[TopicCanonical] {
		snaps, err := LoadStates(ctx, c.brokers, p)
		if err != nil {
			slog.Warn("state restore failed; starting partition empty", "partition", p, "err", err)
		}
		c.runner.Assign(p, snaps)
		slog.Info("partition assigned", "partition", p, "restored_vehicles", len(snaps))
	}
}

func (c *Consumer) revoked(ctx context.Context, _ *kgo.Client, m map[string][]int32) {
	for _, p := range m[TopicCanonical] {
		if err := c.runner.Revoke(ctx, p); err != nil {
			slog.Warn("flush on revoke failed", "partition", p, "err", err)
		}
	}
}

func decode(recs []*kgo.Record, m Metrics) map[int32][]event.Event {
	batch := map[int32][]event.Event{}
	for _, r := range recs {
		var t telemetryv1.TelemetryEvent
		if err := proto.Unmarshal(r.Value, &t); err != nil {
			m.DecodeError()
			continue
		}
		batch[r.Partition] = append(batch[r.Partition], FromProto(&t))
	}
	return batch
}

// poll fetches with a short timeout so periodic work still runs when input is idle.
func poll(ctx context.Context, cl *kgo.Client) []*kgo.Record {
	pctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	fs := cl.PollRecords(pctx, 20_000)
	for _, fe := range fs.Errors() {
		if !errors.Is(fe.Err, context.Canceled) && !errors.Is(fe.Err, context.DeadlineExceeded) {
			slog.Warn("fetch error", "topic", fe.Topic, "partition", fe.Partition, "err", fe.Err)
		}
	}
	return fs.Records()
}

func commit(ctx context.Context, cl *kgo.Client, recs []*kgo.Record, m Metrics) {
	start := time.Now()
	if err := cl.CommitRecords(ctx, recs...); err != nil {
		slog.Warn("commit failed (records will be redelivered; sinks are idempotent)", "err", err)
	}
	m.Commit(time.Since(start))
}

// Run polls, processes, flushes and commits until ctx ends. Processing-time jobs (stale sweep,
// top-K publish) run on this goroutine between batches, so partitions are never touched concurrently.
func (c *Consumer) Run(ctx context.Context, sweepEvery, topEvery time.Duration) error {
	sweep, top := time.NewTicker(sweepEvery), time.NewTicker(topEvery)
	defer sweep.Stop()
	defer top.Stop()
	for ctx.Err() == nil {
		recs := poll(ctx, c.cl)
		if len(recs) > 0 {
			if err := c.runner.Handle(ctx, decode(recs, c.m)); err != nil {
				c.cl.AllowRebalance()
				return err // context ended while Kafka was failing: nothing committed, redelivered later
			}
			commit(ctx, c.cl, recs, c.m)
		}
		select {
		case <-sweep.C:
			if err := c.runner.Sweep(ctx); err != nil {
				slog.Warn("stale sweep flush failed", "err", err)
			}
		case <-top.C:
			c.runner.PublishTopDTCs(ctx)
		default:
		}
		c.cl.AllowRebalance()
	}
	return nil
}

func (c *Consumer) Close() { c.cl.Close() }

// ---- raw sink role ----

const RawGroup = "stream-processor-raw"

// RawConsumer feeds app.RawSink from its own consumer group: a slow raw store shows up as this
// group's lag and never as alert latency.
type RawConsumer struct {
	cl   *kgo.Client
	sink *app.RawSink
	m    Metrics
}

func NewRawConsumer(brokers []string, sink *app.RawSink, m Metrics) (*RawConsumer, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumerGroup(RawGroup), kgo.ConsumeTopics(TopicCanonical),
		kgo.Balancers(kgo.CooperativeStickyBalancer()), kgo.DisableAutoCommit(), kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(200*time.Millisecond))
	if err != nil {
		return nil, err
	}
	return &RawConsumer{cl: cl, sink: sink, m: m}, nil
}

func (c *RawConsumer) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		recs := poll(ctx, c.cl)
		if len(recs) > 0 {
			var evs []event.Event
			for _, part := range decode(recs, c.m) {
				evs = append(evs, part...)
			}
			if err := c.sink.Write(ctx, evs); err != nil {
				c.cl.AllowRebalance()
				return err
			}
			commit(ctx, c.cl, recs, c.m)
		}
		c.cl.AllowRebalance()
	}
	return nil
}

func (c *RawConsumer) Close() { c.cl.Close() }
