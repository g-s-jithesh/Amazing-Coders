package window

import (
	"math"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

func e(tMs int64, soc, odo float64) *event.Event {
	return &event.Event{VIN: "V", TsEventMs: tMs, SoCPct: soc, OdoKm: odo, PackVoltageV: 400, PackCurrentA: 30,
		SpeedKmh: 36, IsolationKohm: 2000, PackTempMaxC: 31, CellVMinMv: 3700, CellVMaxMv: 3720,
		Has: event.HasGPS | event.HasTemp | event.HasCells}
}

func TestRollupValues(t *testing.T) {
	w := New(30_000)
	for i := int64(0); i < 6; i++ {
		w.Add(e(i*10_000, 60-float64(i), 100+float64(i)*0.1))
	}
	if out := w.Close(); len(out) != 0 {
		t.Fatal("minute must stay open until the watermark passes it")
	}
	w.Add(e(90_000, 54, 100.6)) // watermark 60 s → minute 0 closes
	out := w.Close()
	if len(out) != 1 {
		t.Fatalf("closed %d", len(out))
	}
	r := out[0]
	if r.Count != 6 || r.SoCMin != 55 || r.SoCMax != 60 || math.Abs(r.SoCAvg-57.5) > 1e-9 || math.Abs(r.DistanceKm-0.5) > 1e-9 ||
		math.Abs(r.EnergyKWh-400*30/1000.0/60) > 1e-12 || r.SpeedAvgKmh != 36 || r.ImbalanceMaxMv != 20 || r.IsolationMinKohm != 2000 || !r.HasTemp {
		t.Fatalf("rollup %+v", r)
	}
}

func TestLatenessWithinAndBeyond(t *testing.T) {
	w := New(30_000)
	w.Add(e(65_000, 50, 1))
	if !w.Add(e(50_000, 50, 1)) { // 15 s late, minute 0 still open (watermark 35 s)
		t.Fatal("within lateness must be accepted")
	}
	w.Add(e(125_000, 50, 1)) // watermark 95 s → minutes 0 closes, minute 1 still open
	out := w.Close()
	if len(out) != 1 || out[0].MinuteStartMs != 0 {
		t.Fatalf("closed %+v", out)
	}
	if w.Add(e(59_000, 50, 1)) || w.Late != 1 {
		t.Fatal("an event for an emitted minute is late beyond lateness")
	}
}

// A vehicle replaying a 20-minute outage in order is not "late": the watermark is its own.
func TestOutageReplayIsNotLate(t *testing.T) {
	w := New(30_000)
	for tMs := int64(0); tMs <= 20*60_000; tMs += 10_000 {
		if !w.Add(e(tMs, 50, 1)) {
			t.Fatalf("replayed event at %d rejected", tMs)
		}
	}
	if got := len(w.Close()) + len(w.Flush()); got != 21 || w.Late != 0 {
		t.Fatalf("minutes %d late %d", got, w.Late)
	}
}

func TestFlushEmitsOldestFirst(t *testing.T) {
	w := New(30_000)
	w.Add(e(125_000, 1, 1))
	w.Add(e(5_000, 1, 1))
	w.Add(e(65_000, 1, 1))
	out := w.Flush()
	if len(out) != 3 || out[0].MinuteStartMs != 0 || out[2].MinuteStartMs != 120_000 {
		t.Fatalf("flush %+v", out)
	}
}
