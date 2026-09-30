// Package window builds per-vehicle 1-minute event-time rollups with a per-VIN watermark.
//
// Watermark = newest event time seen for this VIN − Lateness. A minute is emitted once the
// watermark passes its end; events for an already-emitted minute are "late beyond lateness":
// counted, never re-opening the window (they are still written to the raw store by the caller).
//
// Why per VIN and not per partition: the fleet contains vehicles with badly set clocks (minutes
// ahead) and vehicles replaying buffered data after an outage (minutes behind, but in order). A
// partition-wide watermark would let one fast clock mark everyone else's events late. Aggregates
// are order-independent, so within-lateness reordering changes nothing. Add is O(1) amortised.
package window

import (
	"math"
	"slices"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

const MinuteMs = 60_000

type Rollup struct {
	VIN                    string
	MinuteStartMs          int64
	Count                  int
	SoCMin, SoCMax, SoCAvg float64
	SpeedAvgKmh            float64
	DistanceKm             float64 // odo max − min inside the minute
	EnergyKWh              float64 // mean V·I × 1 min (+ discharge, − charge)
	ChargeKWhGrid          float64 // mean charge power × 1 min
	TempMaxC               float64
	ImbalanceMaxMv         float64
	IsolationMinKohm       float64
	HasTemp, HasCells      bool
}

type acc struct {
	r                          Rollup
	socSum, speedSum, powerSum float64
	chargeSum                  float64
	odoMin, odoMax             float64
	speedN                     int
}

type Windows struct {
	LatenessMs int64
	maxTs      int64
	emittedTo  int64 // minutes starting before this were emitted
	open       map[int64]*acc
	Late       int
}

func New(latenessMs int64) *Windows { return &Windows{LatenessMs: latenessMs, open: map[int64]*acc{}} }

// Add folds an event in. It returns false if the event was late beyond lateness.
func (w *Windows) Add(ev *event.Event) bool {
	m := ev.TsEventMs - ev.TsEventMs%MinuteMs
	if m < w.emittedTo {
		w.Late++
		return false
	}
	if ev.TsEventMs > w.maxTs {
		w.maxTs = ev.TsEventMs
	}
	a, ok := w.open[m]
	if !ok {
		a = &acc{r: Rollup{VIN: ev.VIN, MinuteStartMs: m, SoCMin: math.Inf(1), SoCMax: math.Inf(-1), IsolationMinKohm: math.Inf(1)},
			odoMin: math.Inf(1), odoMax: math.Inf(-1)}
		w.open[m] = a
	}
	r := &a.r
	r.Count++
	r.SoCMin, r.SoCMax = math.Min(r.SoCMin, ev.SoCPct), math.Max(r.SoCMax, ev.SoCPct)
	a.socSum += ev.SoCPct
	a.powerSum += ev.PackVoltageV * ev.PackCurrentA / 1000
	a.chargeSum += ev.ChargePowerKW
	a.odoMin, a.odoMax = math.Min(a.odoMin, ev.OdoKm), math.Max(a.odoMax, ev.OdoKm)
	r.IsolationMinKohm = math.Min(r.IsolationMinKohm, ev.IsolationKohm)
	if ev.Has&event.HasGPS != 0 {
		a.speedSum += ev.SpeedKmh
		a.speedN++
	}
	if ev.Has&event.HasTemp != 0 {
		r.TempMaxC = math.Max(r.TempMaxC, ev.PackTempMaxC)
		r.HasTemp = true
	}
	if ev.Has&event.HasCells != 0 {
		r.ImbalanceMaxMv = math.Max(r.ImbalanceMaxMv, ev.Imbalance())
		r.HasCells = true
	}
	return true
}

func (a *acc) finish() Rollup {
	r := a.r
	n := float64(r.Count)
	r.SoCAvg = a.socSum / n
	r.EnergyKWh = a.powerSum / n / 60
	r.ChargeKWhGrid = a.chargeSum / n / 60
	r.DistanceKm = a.odoMax - a.odoMin
	if a.speedN > 0 {
		r.SpeedAvgKmh = a.speedSum / float64(a.speedN)
	}
	return r
}

// Close emits every minute that ended before the watermark, oldest first.
func (w *Windows) Close() []Rollup { return w.closeBefore(w.maxTs - w.LatenessMs) }

// Flush emits all open minutes (idle vehicle, partition revoke, shutdown).
func (w *Windows) Flush() []Rollup { return w.closeBefore(math.MaxInt64) }

func (w *Windows) closeBefore(watermark int64) []Rollup {
	var due []int64
	for m := range w.open {
		if m+MinuteMs <= watermark {
			due = append(due, m)
		}
	}
	if len(due) == 0 {
		return nil
	}
	slices.Sort(due) // oldest first
	out := make([]Rollup, 0, len(due))
	for _, m := range due {
		out = append(out, w.open[m].finish())
		delete(w.open, m)
		if m+MinuteMs > w.emittedTo {
			w.emittedTo = m + MinuteMs
		}
	}
	return out
}
