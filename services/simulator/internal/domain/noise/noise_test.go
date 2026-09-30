package noise

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/vin"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

var testVIN = func() string {
	v, err := vin.Build("0KC", "DV45N", 2024, 'C', 1)
	if err != nil {
		panic(err)
	}
	return v
}()

func clean(seq uint64, nowMs int64) telemetry.Sample {
	return telemetry.Sample{VIN: testVIN, Seq: seq, TsEventMs: nowMs, Lat: 13.08, Lon: 80.27, SoCPct: 55, SchemaVersion: 1}
}

type arrival struct {
	s     telemetry.Sample
	atMs  int64
	fresh bool
}

// feed pushes n samples at 1 Hz (then drains for a minute) and returns every output with its arrival time.
func feed(c *Channel, n int) []arrival {
	var out []arrival
	var buf []telemetry.Sample
	for i := 0; i < n+60; i++ {
		now := int64(i) * 1000
		buf = buf[:0]
		if i < n {
			buf = c.Push(clean(uint64(i+1), now), now, buf)
		} else {
			buf = c.Flush(now, buf)
		}
		for _, s := range buf {
			out = append(out, arrival{s, now, true})
		}
	}
	return out
}

func near(got, want float64) bool { return math.Abs(got-want) <= 0.1*want }

// CLAUDE.md simulator invariant 5: noise rates within ±10 % of configured over 1M events.
func TestRatesWithinTenPercent(t *testing.T) {
	p := Realistic
	p.OutagesPerDay = 0 // measured separately
	c := NewChannel(p, rand.New(rand.NewPCG(1, 2)))
	const n = 1_000_000
	out := feed(c, n)

	seen := map[uint64]int{}
	var dups, reordered, dropouts, maxSeq uint64
	malformed := map[telemetry.Malformed]int{}
	for _, a := range out {
		s := a.s
		if seen[s.Seq]++; seen[s.Seq] == 2 {
			dups++
		}
		if s.Seq < maxSeq {
			reordered++ // arrived after a later sample
		}
		maxSeq = max(maxSeq, s.Seq)
		if s.Dropout != 0 {
			dropouts++
		}
		if s.Malformed != telemetry.WellFormed && seen[s.Seq] == 1 {
			malformed[s.Malformed]++
		}
	}
	if len(seen) != n {
		t.Fatalf("lost samples: %d unique of %d", len(seen), n)
	}
	totalMal := 0
	for _, k := range malformed {
		totalMal += k
	}
	for name, got := range map[string][2]float64{
		"duplicates": {float64(dups) / n, p.DupRate},
		"reordered":  {float64(reordered) / n, p.ReorderRate},
		"dropouts":   {float64(dropouts) / float64(len(out)), p.DropoutRate},
		"malformed":  {float64(totalMal) / n, p.MalformedRate},
	} {
		if !near(got[0], got[1]) {
			t.Errorf("%s rate %.5f, configured %.5f (±10%%)", name, got[0], got[1])
		}
	}
	for k := telemetry.MalVINChecksum; k <= telemetry.MalDecode; k++ {
		if malformed[k] == 0 {
			t.Errorf("malformed kind %d never produced", k)
		}
	}
}

// Each malformed kind corrupts exactly what its DLQ reason says.
func TestMalformedKindsAreExact(t *testing.T) {
	p := Profile{MalformedRate: 1}
	c := NewChannel(p, rand.New(rand.NewPCG(3, 4)))
	for i := 0; i < 5000; i++ {
		out := c.Push(clean(uint64(i), int64(i)*1000), int64(i)*1000, nil)
		s := out[0]
		vinOK := vin.Valid(s.VIN)
		switch s.Malformed {
		case telemetry.MalVINChecksum:
			if vinOK || len(s.VIN) != 17 || s.VIN[:8] != testVIN[:8] || s.VIN[9:] != testVIN[9:] {
				t.Fatalf("VIN checksum corruption wrong: %q", s.VIN)
			}
		case telemetry.MalDTCFormat:
			if !vinOK || len(s.DTC) != 1 || s.DTC[0] != "PX12Z" {
				t.Fatalf("DTC corruption wrong: %+v", s)
			}
		case telemetry.MalSchemaVersion:
			if !vinOK || s.SchemaVersion != 99 {
				t.Fatalf("schema corruption wrong: %+v", s)
			}
		case telemetry.MalRange:
			if !vinOK || s.SoCPct <= 100 {
				t.Fatalf("range corruption wrong: %+v", s)
			}
		case telemetry.MalDecode:
			if !vinOK || s.SoCPct != 55 || s.SchemaVersion != 1 {
				t.Fatalf("decode kind must leave fields intact (encoder truncates): %+v", s)
			}
		default:
			t.Fatalf("malformed rate 1 produced kind %d", s.Malformed)
		}
	}
}

func TestReorderedNeverLaterThanMax(t *testing.T) {
	p := Profile{ReorderRate: 0.3, MaxLateMs: 30_000}
	for _, a := range feed(NewChannel(p, rand.New(rand.NewPCG(5, 6))), 50_000) {
		if late := a.atMs - a.s.TsEventMs; late < 0 || late > p.MaxLateMs {
			t.Fatalf("seq %d arrived %d ms late", a.s.Seq, late)
		}
	}
}

func TestCleanProfileIsIdentity(t *testing.T) {
	c := NewChannel(Profiles["clean"], rand.New(rand.NewPCG(7, 8)))
	out := feed(c, 10_000)
	if len(out) != 10_000 {
		t.Fatalf("clean emitted %d of 10000", len(out))
	}
	for i, a := range out {
		if want := clean(uint64(i+1), int64(i)*1000); !reflect.DeepEqual(a.s, want) || a.atMs != a.s.TsEventMs {
			t.Fatalf("clean profile altered sample %d: %+v", i, a.s)
		}
	}
}

// Outages lose nothing (up to the buffer cap): samples are held, then replayed in order as a burst.
func TestOutageBuffersAndReplaysInOrder(t *testing.T) {
	p := Profile{OutagesPerDay: 24, OutageMinMs: 10 * 60_000, OutageMaxMs: 10 * 60_000}
	out := feed(NewChannel(p, rand.New(rand.NewPCG(9, 10))), 6*3600)
	if len(out) != 6*3600 {
		t.Fatalf("emitted %d of %d", len(out), 6*3600)
	}
	biggest, burst := 0, 0
	for i := range out {
		if out[i].s.Seq != uint64(i+1) {
			t.Fatalf("order broken at %d", i)
		}
		if i > 0 && out[i].atMs == out[i-1].atMs {
			burst++
		} else {
			burst = 1
		}
		biggest = max(biggest, burst)
	}
	if biggest < 600 { // a 10-minute outage at 1 Hz replays ≥ 600 samples at once
		t.Fatalf("largest burst %d samples", biggest)
	}
}

func TestOutageBufferCapDropsOldest(t *testing.T) {
	c := NewChannel(Profile{}, rand.New(rand.NewPCG(1, 1)))
	c.started, c.nextOutage, c.outageUntilMs = true, math.MaxInt64, math.MaxInt64-1
	for i := 0; i < MaxBuffered+5; i++ {
		c.Push(clean(uint64(i+1), 0), 0, nil)
	}
	if len(c.buffered) != MaxBuffered || c.buffered[0].Seq != 6 {
		t.Fatalf("buffer len %d first seq %d", len(c.buffered), c.buffered[0].Seq)
	}
}

func TestDeterministic(t *testing.T) {
	run := func() []arrival { return feed(NewChannel(Profiles["hostile"], rand.New(rand.NewPCG(11, 12))), 20_000) }
	a, b := run(), run()
	if !slices.EqualFunc(a, b, func(x, y arrival) bool { return x.atMs == y.atMs && x.s.Seq == y.s.Seq && x.s.Lat == y.s.Lat }) {
		t.Fatal("same seed gave different noise")
	}
}

func TestClockSkew(t *testing.T) {
	big := 0
	const n = 20_000
	for i := 0; i < n; i++ {
		c := NewChannel(Realistic, rand.New(rand.NewPCG(uint64(i), 13)))
		if abs(c.SkewMs()) > 60_000 {
			big++
		}
	}
	if got := float64(big) / n; !near(got, Realistic.BigSkewVehicleRate) && math.Abs(got-Realistic.BigSkewVehicleRate) > 0.002 {
		t.Fatalf("big-skew vehicle share %.4f, want ≈ %.4f", got, Realistic.BigSkewVehicleRate)
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestProfileByNameAndBreakCheckDigit(t *testing.T) {
	for name := range Profiles {
		if _, err := ProfileByName(name); err != nil {
			t.Error(err)
		}
	}
	if _, err := ProfileByName("chaos"); err == nil {
		t.Error("want error")
	}
	for k := 0; k < 10; k++ {
		if b := BreakCheckDigit(testVIN, k); vin.Valid(b) || b == testVIN {
			t.Errorf("k=%d still valid: %s", k, b)
		}
	}
	if BreakCheckDigit("SHORT", 1) != "SHORT" {
		t.Error("non-17 VIN must be returned unchanged")
	}
}
