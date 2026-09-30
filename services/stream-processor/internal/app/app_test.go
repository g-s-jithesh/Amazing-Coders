package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/sketch"
)

const vin = "0KCDV45N9RC000001"

type reg map[string]*event.Vehicle

func (r reg) Get(v string) (*event.Vehicle, bool) { x, ok := r[v]; return x, ok }

type fakeRaw struct {
	mu    sync.Mutex
	fails int
	n     int
}

func (f *fakeRaw) Write(_ context.Context, evs []event.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errors.New("scylla timeout")
	}
	f.n += len(evs)
	return nil
}

type fakeEmit struct {
	mu     sync.Mutex
	alerts []AlertOut
	sess   []SessionOut
	roll   int
	states []StateOut
	fails  int
}

func (f *fakeEmit) Emit(_ context.Context, o Outputs) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errors.New("kafka down")
	}
	f.alerts = append(f.alerts, o.Alerts...)
	f.sess = append(f.sess, o.Sessions...)
	f.roll += len(o.Rollups)
	f.states = append(f.states, o.States...)
	return nil
}

type fakeLive struct {
	mu   sync.Mutex
	n    int
	fail bool
	top  map[string][]sketch.Item
}

func (f *fakeLive) Update(_ context.Context, l []LiveOut) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("redis down")
	}
	f.n += len(l)
	return nil
}

func (f *fakeLive) TopDTCs(_ context.Context, tenant string, _ int64, top []sketch.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.top == nil {
		f.top = map[string][]sketch.Item{}
	}
	f.top[tenant] = top
	return nil
}

type obs struct {
	mu        sync.Mutex
	sinkErr   map[string]int
	retries   int
	unknown   int
	lateRules int
}

func (o *obs) Batch(x *Outputs, _ time.Duration) {
	o.mu.Lock()
	o.unknown += x.Unknown
	o.lateRules += x.LateRule
	o.mu.Unlock()
}
func (o *obs) SinkError(s string)             { o.mu.Lock(); o.sinkErr[s]++; o.mu.Unlock() }
func (o *obs) FlushRetry()                    { o.mu.Lock(); o.retries++; o.mu.Unlock() }
func (o *obs) SinkTime(string, time.Duration) {}

func engine(t *testing.T) *rules.Engine {
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c rules.Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	c.DTCSeverity = map[string]string{"P0A7E": "critical"}
	return rules.New(c)
}

type rig struct {
	r    *Runner
	sink *RawSink
	raw  *fakeRaw
	emit *fakeEmit
	live *fakeLive
	obs  *obs
	now  time.Time
}

func newRig(t *testing.T) *rig {
	g := &rig{raw: &fakeRaw{}, emit: &fakeEmit{}, live: &fakeLive{}, obs: &obs{sinkErr: map[string]int{}},
		now: time.Date(2026, 9, 1, 4, 30, 0, 0, time.UTC)} // 10:00 IST
	g.sink = &RawSink{Store: g.raw, Obs: g.obs}
	g.r = &Runner{Engine: engine(t), Emit: g.emit, Live: g.live, Obs: g.obs, Now: func() time.Time { return g.now },
		Registry: reg{vin: {VIN: vin, TenantID: "T1", FleetID: "F1", DepotLat: 13.08, DepotLon: 80.27, CapacityKWh: 45, WhPerKm: 190, NominalV: 350,
			DepartMinIST: 6 * 60, ReturnMinIST: 14 * 60}}}
	return g
}

// handle runs one batch through both roles (processor and raw sink), as the two consumer groups do.
func (g *rig) handle(ctx context.Context, b map[int32][]event.Event) error {
	if err := g.r.Handle(ctx, b); err != nil {
		return err
	}
	var evs []event.Event
	for _, part := range b {
		evs = append(evs, part...)
	}
	return g.sink.Write(ctx, evs)
}

func sample(tSec int64) event.Event {
	return event.Event{VIN: vin, TsEventMs: 1_788_238_800_000 + tSec*1000, TsIngestMs: 1_788_238_800_000 + tSec*1000 + 40,
		SoCPct: 60, PackVoltageV: 360, PackCurrentA: 20, PackTempMinC: 30, PackTempMaxC: 32, CellVMinMv: 3700, CellVMaxMv: 3710,
		IsolationKohm: 2500, HVInterlockOK: true, Aux12vV: 13.9, AmbientC: 30, ChargeState: 1, SpeedKmh: 40, Lat: 13.1, Lon: 80.3,
		Evt: 1, Has: event.HasGPS | event.HasTemp | event.HasCells}
}

func TestDTCAlertIsEnrichedAndTimed(t *testing.T) {
	g := newRig(t)
	e := sample(0)
	e.DTC = []string{"P0A7E"}
	if err := g.handle(context.Background(), map[int32][]event.Event{2: {e}}); err != nil {
		t.Fatal(err)
	}
	if len(g.emit.alerts) != 1 {
		t.Fatalf("alerts %+v", g.emit.alerts)
	}
	a := g.emit.alerts[0]
	if a.RuleID != rules.DTCRaised || a.Severity != "critical" || a.TenantID != "T1" || a.FleetID != "F1" ||
		a.TriggerIngestMs != e.TsIngestMs || a.ProcessedMs != g.now.UnixMilli() || a.Detail != "P0A7E" {
		t.Fatalf("alert %+v", a)
	}
	if g.raw.n != 1 || len(g.emit.states) != 1 || g.live.n != 1 {
		t.Fatalf("raw=%d states=%d live=%d", g.raw.n, len(g.emit.states), g.live.n)
	}
	g.r.PublishTopDTCs(context.Background())
	if top := g.live.top["T1"]; len(top) != 1 || top[0].Key != "P0A7E" {
		t.Fatalf("top-K %+v", g.live.top)
	}
}

func TestLiveThrottledAndUnknownCounted(t *testing.T) {
	g := newRig(t)
	evs := []event.Event{sample(0), sample(1), sample(2)}
	stranger := sample(3)
	stranger.VIN = "0KAXXXXX0XX000000"
	evs = append(evs, stranger)
	_ = g.handle(context.Background(), map[int32][]event.Event{0: evs})
	if g.live.n != 2 || g.obs.unknown != 1 { // one per vehicle within the same second
		t.Fatalf("live=%d unknown=%d", g.live.n, g.obs.unknown)
	}
	g.now = g.now.Add(1500 * time.Millisecond)
	_ = g.handle(context.Background(), map[int32][]event.Event{0: {sample(4)}})
	if g.live.n != 3 {
		t.Fatalf("live after 1.5 s = %d", g.live.n)
	}
}

// A failing sink is retried; processing is not repeated, so nothing is emitted twice.
func TestFlushRetriesWithoutReprocessing(t *testing.T) {
	g := newRig(t)
	g.raw.fails, g.emit.fails = 2, 1
	e := sample(0)
	e.DTC = []string{"P0A7E"}
	if err := g.handle(context.Background(), map[int32][]event.Event{0: {e}}); err != nil {
		t.Fatal(err)
	}
	if len(g.emit.alerts) != 1 || g.raw.n != 1 || g.obs.retries != 3 || g.obs.sinkErr["scylla"] != 2 || g.obs.sinkErr["kafka"] != 1 {
		t.Fatalf("alerts=%d raw=%d retries=%d errs=%v", len(g.emit.alerts), g.raw.n, g.obs.retries, g.obs.sinkErr)
	}
}

func TestRedisDownDoesNotBlock(t *testing.T) {
	g := newRig(t)
	g.live.fail = true
	if err := g.handle(context.Background(), map[int32][]event.Event{0: {sample(0)}}); err != nil {
		t.Fatal(err)
	}
	if g.obs.sinkErr["redis"] != 1 || g.raw.n != 1 {
		t.Fatalf("errs=%v raw=%d", g.obs.sinkErr, g.raw.n)
	}
}

func TestPersistentFailureBlocksUntilCancelled(t *testing.T) {
	g := newRig(t)
	g.raw.fails = 1 << 30
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := g.handle(ctx, map[int32][]event.Event{0: {sample(0)}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded (no commit), got %v", err)
	}
}

func TestRevokeFlushesAndAssignRestores(t *testing.T) {
	g := newRig(t)
	hot := func(s int64) event.Event { e := sample(s); e.PackTempMaxC = 60; return e }
	var evs []event.Event
	for s := int64(0); s <= 60; s += 10 {
		evs = append(evs, hot(s))
	}
	_ = g.handle(context.Background(), map[int32][]event.Event{5: evs})
	fired := len(g.emit.alerts)
	if fired != 2 {
		t.Fatalf("setup alerts %d", fired)
	}
	if err := g.r.Revoke(context.Background(), 5); err != nil || g.emit.roll != 2 { // two open minutes flushed
		t.Fatalf("revoke err=%v rollups=%d", err, g.emit.roll)
	}
	last := g.emit.states[len(g.emit.states)-1]
	g.r.Assign(5, []StateOut{last})
	_ = g.handle(context.Background(), map[int32][]event.Event{5: {hot(70), hot(80)}})
	if len(g.emit.alerts) != fired {
		t.Fatalf("restored partition re-fired: %+v", g.emit.alerts[fired:])
	}
	if p, v := g.r.Stats(); p != 1 || v != 1 {
		t.Fatalf("stats %d/%d", p, v)
	}
}

func TestSweepStaleOnlyOnDuty(t *testing.T) {
	g := newRig(t)
	_ = g.handle(context.Background(), map[int32][]event.Event{0: {sample(0)}})
	g.now = g.now.Add(11 * time.Minute) // 10:11 IST, inside the 06:00–14:00 duty
	if err := g.r.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(g.emit.alerts); n != 1 || g.emit.alerts[0].RuleID != rules.TelemetryStale || g.emit.alerts[0].TenantID != "T1" {
		t.Fatalf("stale: %+v", g.emit.alerts)
	}
	g2 := newRig(t)
	g2.now = time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC) // 19:30 IST, off duty
	_ = g2.handle(context.Background(), map[int32][]event.Event{0: {sample(0)}})
	g2.now = g2.now.Add(30 * time.Minute)
	_ = g2.r.Sweep(context.Background())
	if len(g2.emit.alerts) != 0 {
		t.Fatal("off-duty silence must not alert")
	}
}

func TestSessionsCarryTenantAndLateEventsCounted(t *testing.T) {
	g := newRig(t)
	plug := sample(0)
	plug.PackCurrentA, plug.ChargeState, plug.Evt, plug.SpeedKmh = -30, 2, event.EvtPlugIn, 0
	later := sample(20)
	late := sample(10) // older than the newest processed event for this VIN
	_ = g.handle(context.Background(), map[int32][]event.Event{0: {plug, later, late}})
	if len(g.emit.sess) == 0 || g.emit.sess[0].TenantID != "T1" {
		t.Fatalf("sessions %+v", g.emit.sess)
	}
	if g.obs.lateRules != 1 || g.raw.n != 3 {
		t.Fatalf("late=%d raw=%d (late events are still stored)", g.obs.lateRules, g.raw.n)
	}
}

// The point of the role split: an alert reaches Kafka while the raw store is down.
func TestAlertsDoNotDependOnRawStore(t *testing.T) {
	g := newRig(t)
	g.raw.fails = 1 << 30
	e := sample(0)
	e.DTC = []string{"P0A7E"}
	if err := g.r.Handle(context.Background(), map[int32][]event.Event{0: {e}}); err != nil {
		t.Fatal(err)
	}
	if len(g.emit.alerts) != 1 {
		t.Fatalf("alerts %d", len(g.emit.alerts))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := g.sink.Write(ctx, []event.Event{e}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("raw sink must keep retrying (no commit) until ctx ends, got %v", err)
	}
	if err := g.sink.Write(context.Background(), nil); err != nil {
		t.Fatal("empty batch is a no-op")
	}
}
