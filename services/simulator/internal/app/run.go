package app

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/noise"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

// Publisher sends one encoded payload for a vehicle. It takes ownership of payload and must be
// safe for concurrent use. Implementations may block to apply back-pressure.
type Publisher interface {
	Publish(ctx context.Context, oem, vin string, payload []byte) error
	Close() error
}

// TruthSink receives ground truth (never the pipeline). May be nil.
type TruthSink interface {
	Faults([]vehicle.FaultTruth) error
	SoH([]SoHTruth) error
}

// EncodeFunc renders a (possibly noisy) sample in its vehicle's OEM wire format.
type EncodeFunc func(s *telemetry.Sample) ([]byte, error)

type RunConfig struct {
	Seed     uint64
	Start    time.Time     // sim start
	Dt       float64       // sim seconds per tick
	RateHz   float64       // periodic samples per vehicle per sim second
	Speedup  float64       // sim seconds per wall second; 0 = as fast as possible
	Duration time.Duration // sim time to run; 0 = until ctx is cancelled
	Workers  int
	Noise    noise.Profile

	// Shift-start burst (CLAUDE.md §6): after IGN_ON a vehicle reports BurstFactor× faster for BurstFor.
	BurstFactor int
	BurstFor    time.Duration
}

type Stats struct {
	Ticks, Samples, Published, PublishErrors, EncodeErrors atomic.Int64
	SimNowMs                                               atomic.Int64
}

type slot struct {
	v          *vehicle.Vehicle
	ch         *noise.Channel
	phase      int64
	burstUntil int64
}

// Run drives the fleet: step → sample (periodic + every event) → noise → encode → publish, one
// worker per contiguous slice of vehicles. Ground truth is drained each tick; true SoH is
// snapshotted at run start and at each IST midnight. Returns when Duration elapses (sim time) or ctx is cancelled.
func Run(ctx context.Context, vs []*vehicle.Vehicle, cfg RunConfig, encode EncodeFunc, pub Publisher, truth TruthSink, st *Stats) error {
	if cfg.Dt <= 0 || cfg.RateHz <= 0 {
		return fmt.Errorf("app: dt and rate must be > 0")
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.BurstFactor < 1 {
		cfg.BurstFactor = 1
	}
	period := max(1, int64(math.Round(1/(cfg.RateHz*cfg.Dt)))) // ticks between periodic samples
	burstPeriod := max(1, period/int64(cfg.BurstFactor))

	slots := make([]slot, len(vs))
	for i, v := range vs {
		h := fnv.New64a()
		_, _ = h.Write([]byte(v.VIN))
		slots[i] = slot{v: v, ch: noise.NewChannel(cfg.Noise, NoiseRNG(cfg.Seed, v.VIN)), phase: int64(h.Sum64() % uint64(period))}
	}
	chunk := (len(slots) + cfg.Workers - 1) / cfg.Workers
	var parts [][]slot
	for lo := 0; lo < len(slots); lo += chunk {
		parts = append(parts, slots[lo:min(lo+chunk, len(slots))])
	}

	wallStart := time.Now()
	lastDay := int64(math.MinInt64) // snapshot SoH on the first tick (run baseline), then each IST midnight
	for tick := int64(0); ; tick++ {
		simElapsed := time.Duration(float64(tick) * cfg.Dt * float64(time.Second))
		if cfg.Duration > 0 && simElapsed >= cfg.Duration {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return nil
		}
		now := cfg.Start.Add(simElapsed)
		tk := TickAt(now, cfg.Dt)
		amb := map[string]float64{}
		for _, c := range masterdata.Cities {
			amb[c.Name] = env.AmbientC(c.Name, now)
		}

		var wg sync.WaitGroup
		for _, part := range parts {
			wg.Add(1)
			go func(part []slot) {
				defer wg.Done()
				var out []telemetry.Sample
				for i := range part {
					sl := &part[i]
					sl.v.Step(tk, amb[sl.v.City])
					p := period
					if tk.NowMs < sl.burstUntil {
						p = burstPeriod
					}
					out = out[:0]
					for periodic := (tick+sl.phase)%p == 0; periodic || sl.v.PendingEvents() > 0; periodic = false {
						s := sl.v.Sample(tk.NowMs)
						if s.Evt == telemetry.EvtIgnOn {
							sl.burstUntil = tk.NowMs + cfg.BurstFor.Milliseconds()
						}
						st.Samples.Add(1)
						out = sl.ch.Push(s, tk.NowMs, out)
					}
					out = sl.ch.Flush(tk.NowMs, out)
					for j := range out {
						b, err := encode(&out[j])
						if err != nil {
							st.EncodeErrors.Add(1)
							continue
						}
						if err := pub.Publish(ctx, sl.v.OEM, sl.v.VIN, b); err != nil {
							st.PublishErrors.Add(1)
							continue
						}
						st.Published.Add(1)
					}
				}
			}(part)
		}
		wg.Wait()
		st.Ticks.Add(1)
		st.SimNowMs.Store(tk.NowMs)

		if truth != nil {
			if err := truth.Faults(DrainFaultTruth(vs)); err != nil {
				return fmt.Errorf("ground truth: %w", err)
			}
			if tk.DayIST != lastDay {
				lastDay = tk.DayIST
				if err := truth.SoH(SnapshotSoH(vs, now)); err != nil {
					return fmt.Errorf("ground truth: %w", err)
				}
			}
		}

		if cfg.Speedup > 0 {
			due := wallStart.Add(time.Duration(float64(simElapsed+time.Duration(cfg.Dt*float64(time.Second))) / cfg.Speedup))
			if d := time.Until(due); d > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(d):
				}
			}
		}
	}
}
