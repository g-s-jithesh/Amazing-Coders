package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/sketch"
)

// ---- ports ----

// RawStore is the hot raw store (Scylla). Writes must be idempotent upserts.
type RawStore interface {
	Write(ctx context.Context, evs []event.Event) error
}

// Emitter produces alerts, sessions, rollups and state snapshots to Kafka and waits for acks.
type Emitter interface {
	Emit(ctx context.Context, o Outputs) error
}

// Live is the live-map store (Redis). Best effort: failures degrade the map, never the pipeline.
type Live interface {
	Update(ctx context.Context, live []LiveOut) error
	TopDTCs(ctx context.Context, tenant string, hourStartMs int64, top []sketch.Item) error
}

// Observer receives metrics.
type Observer interface {
	Batch(o *Outputs, processing time.Duration)
	SinkError(sink string)
	FlushRetry()
	SinkTime(sink string, d time.Duration)
}

// ---- runner ----

type Runner struct {
	Engine   *rules.Engine
	Registry Registry
	Emit     Emitter
	Live     Live
	Obs      Observer
	Now      func() time.Time

	mu    sync.Mutex
	parts map[int32]*Partition
	topMu sync.Mutex
	top   map[topKey]*sketch.TopK
}

type topKey struct {
	tenant string
	hourMs int64
}

func (r *Runner) init() {
	if r.parts == nil {
		r.parts = map[int32]*Partition{}
		r.top = map[topKey]*sketch.TopK{}
	}
	if r.Now == nil {
		r.Now = time.Now
	}
}

// Assign creates the state for a newly owned partition, seeded from vehicle.state.v1 snapshots.
func (r *Runner) Assign(id int32, snaps []StateOut) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	p := NewPartition(id, r.Engine, r.Registry)
	p.Restore(snaps)
	r.parts[id] = p
}

// Revoke flushes the partition's open rollup windows and drops its state.
func (r *Runner) Revoke(ctx context.Context, id int32) error {
	r.mu.Lock()
	p := r.parts[id]
	delete(r.parts, id)
	r.mu.Unlock()
	if p == nil {
		return nil
	}
	return r.flush(ctx, p.Flush())
}

func (r *Runner) partition(id int32) *Partition {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	p, ok := r.parts[id]
	if !ok { // assignment callback not seen (e.g. tests): start empty
		p = NewPartition(id, r.Engine, r.Registry)
		r.parts[id] = p
	}
	return p
}

// Handle processes one poll batch (events grouped by partition, each in offset order) and returns
// once the outputs are acknowledged by Kafka (the live map is best effort); the caller then commits.
// The raw store is not written here: that is the separate raw-sink role (RawSink), with its own
// consumer group, so bulk-storage throughput can never delay an alert.
func (r *Runner) Handle(ctx context.Context, batch map[int32][]event.Event) error {
	start := time.Now()
	nowMs := r.Now().UnixMilli()
	results := make([]Outputs, 0, len(batch))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id, evs := range batch {
		p := r.partition(id)
		wg.Add(1)
		go func(p *Partition, evs []event.Event) {
			defer wg.Done()
			o := p.Process(evs, nowMs)
			mu.Lock()
			results = append(results, o)
			mu.Unlock()
		}(p, evs)
	}
	wg.Wait()
	var all Outputs
	for _, o := range results {
		all.Merge(o)
	}
	r.countDTCs(all.DTCs, nowMs)
	r.Obs.Batch(&all, time.Since(start))
	return r.flush(ctx, all)
}

// retry runs fn until it succeeds, with capped exponential backoff, or until ctx ends.
func retry(ctx context.Context, obs Observer, sink string, fn func() error) error {
	backoff := 100 * time.Millisecond
	for {
		err := fn()
		if err == nil {
			return nil
		}
		obs.SinkError(sink)
		obs.FlushRetry()
		slog.Warn("sink write failed; retrying (progress paused)", "sink", sink, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 5*time.Second)
	}
}

// Sweep runs processing-time checks (TELEMETRY_STALE) on every owned partition.
func (r *Runner) Sweep(ctx context.Context) error {
	r.mu.Lock()
	r.init()
	parts := make([]*Partition, 0, len(r.parts))
	for _, p := range r.parts {
		parts = append(parts, p)
	}
	r.mu.Unlock()
	var all Outputs
	now := r.Now()
	for _, p := range parts {
		all.Merge(p.Sweep(now))
	}
	if len(all.Alerts) == 0 {
		return nil
	}
	return r.flush(ctx, all)
}

// flush writes the latency-critical outputs: Kafka must succeed (retried until ctx ends:
// back-pressure, never skipping); the live map is best effort and never blocks the pipeline.
func (r *Runner) flush(ctx context.Context, o Outputs) error {
	var wg sync.WaitGroup
	if len(o.Live) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			if err := r.Live.Update(ctx, o.Live); err != nil {
				r.Obs.SinkError("redis")
			}
			r.Obs.SinkTime("redis", time.Since(start))
		}()
	}
	start := time.Now()
	err := retry(ctx, r.Obs, "kafka", func() error { return r.Emit.Emit(ctx, o) })
	r.Obs.SinkTime("kafka", time.Since(start))
	wg.Wait()
	return err
}

func hourStart(ms int64) int64 { return ms - ms%3_600_000 }

func (r *Runner) countDTCs(hits []DTCHit, nowMs int64) {
	if len(hits) == 0 {
		return
	}
	r.topMu.Lock()
	defer r.topMu.Unlock()
	r.init()
	h := hourStart(nowMs)
	for _, x := range hits {
		k := topKey{x.TenantID, h}
		t, ok := r.top[k]
		if !ok {
			t = sketch.NewTopK(10, 0.001, 0.01)
			r.top[k] = t
		}
		t.Add(x.Code)
	}
}

// PublishTopDTCs writes the current hour's top-K per tenant to the live store and drops older hours.
func (r *Runner) PublishTopDTCs(ctx context.Context) {
	r.topMu.Lock()
	r.init()
	h := hourStart(r.Now().UnixMilli())
	snap := map[topKey][]sketch.Item{}
	for k, t := range r.top {
		if k.hourMs < h-3_600_000 { // keep the current and previous hour
			delete(r.top, k)
			continue
		}
		snap[k] = t.Top()
	}
	r.topMu.Unlock()
	for k, items := range snap {
		if err := r.Live.TopDTCs(ctx, k.tenant, k.hourMs, items); err != nil {
			r.Obs.SinkError("redis")
		}
	}
}

// Stats reports owned partitions and vehicles (metrics).
func (r *Runner) Stats() (partitions, vehicles int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.parts {
		vehicles += p.Vehicles()
	}
	return len(r.parts), vehicles
}
