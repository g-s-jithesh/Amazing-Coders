// Package noise degrades clean samples the way real telematics does, so the pipeline has to cope:
// duplicates, out-of-order arrival (≤ MaxLate), GPS jitter, sensor dropouts, clock skew, network
// outages replayed as bursts, and malformed payloads of known kinds (CLAUDE.md §6).
//
// A Channel is per vehicle with its own RNG, so noise is deterministic per VIN regardless of
// sharding. Push is O(1) amortised; an outage buffers at most MaxBuffered samples.
package noise

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

type Profile struct {
	DupRate, ReorderRate, DropoutRate, MalformedRate float64 // per sample
	MaxLateMs                                        int64   // reordered samples arrive up to this late
	GPSJitterM                                       float64 // σ of GPS error
	ClockSkewSdMs                                    float64 // per-vehicle constant skew σ
	BigSkewVehicleRate                               float64 // fraction of vehicles with a badly set clock
	BigSkewMs                                        int64
	OutagesPerDay                                    float64
	OutageMinMs, OutageMaxMs                         int64
}

// Realistic rates are the ones CLAUDE.md §6 asks the pipeline to survive.
var Realistic = Profile{
	DupRate: 0.01, ReorderRate: 0.02, DropoutRate: 0.005, MalformedRate: 0.001,
	MaxLateMs: 30_000, GPSJitterM: 5, ClockSkewSdMs: 2000, BigSkewVehicleRate: 0.005, BigSkewMs: 5 * 60_000,
	OutagesPerDay: 1.0 / 3, OutageMinMs: 5 * 60_000, OutageMaxMs: 30 * 60_000,
}

// Profiles selectable with --noise.
var Profiles = map[string]Profile{
	"clean":     {},
	"realistic": Realistic,
	"hostile": {
		DupRate: 0.05, ReorderRate: 0.10, DropoutRate: 0.02, MalformedRate: 0.005,
		MaxLateMs: 30_000, GPSJitterM: 15, ClockSkewSdMs: 5000, BigSkewVehicleRate: 0.02, BigSkewMs: 15 * 60_000,
		OutagesPerDay: 2, OutageMinMs: 5 * 60_000, OutageMaxMs: 60 * 60_000,
	},
}

func ProfileByName(name string) (Profile, error) {
	p, ok := Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("noise: unknown profile %q (clean|realistic|hostile)", name)
	}
	return p, nil
}

// MaxBuffered caps the samples held during one outage (≈ 3 h at 1 Hz); older ones are dropped
// as a real device's ring buffer would.
const MaxBuffered = 10_800

type held struct {
	releaseMs int64
	s         telemetry.Sample
}

type Channel struct {
	p                         Profile
	rng                       *rand.Rand
	skewMs                    int64
	held                      []held
	buffered                  []telemetry.Sample
	outageUntilMs, nextOutage int64
	started                   bool
}

func NewChannel(p Profile, rng *rand.Rand) *Channel {
	c := &Channel{p: p, rng: rng}
	c.skewMs = int64(rng.NormFloat64() * p.ClockSkewSdMs)
	if rng.Float64() < p.BigSkewVehicleRate {
		c.skewMs += p.BigSkewMs * int64(1-2*rng.IntN(2))
	}
	return c
}

// SkewMs is this vehicle's constant clock offset.
func (c *Channel) SkewMs() int64 { return c.skewMs }

func (c *Channel) scheduleOutage(nowMs int64) {
	if c.p.OutagesPerDay <= 0 {
		c.nextOutage = math.MaxInt64
		return
	}
	c.nextOutage = nowMs + int64(c.rng.ExpFloat64()/c.p.OutagesPerDay*86_400_000)
}

// Push takes one clean sample produced at wall time nowMs and appends to out whatever the vehicle
// transmits now (zero, one or many samples). Call Flush on ticks without a new sample so held
// samples are still released.
func (c *Channel) Push(s telemetry.Sample, nowMs int64, out []telemetry.Sample) []telemetry.Sample {
	if !c.started {
		c.started = true
		c.scheduleOutage(nowMs)
	}
	c.corrupt(&s)

	// Network outage: buffer, then replay everything in one burst when the link returns.
	if nowMs >= c.nextOutage && c.outageUntilMs <= nowMs {
		c.outageUntilMs = nowMs + c.p.OutageMinMs + c.rng.Int64N(c.p.OutageMaxMs-c.p.OutageMinMs+1)
		c.scheduleOutage(c.outageUntilMs)
	}
	if nowMs < c.outageUntilMs {
		if len(c.buffered) == MaxBuffered {
			c.buffered = c.buffered[1:]
		}
		c.buffered = append(c.buffered, s)
		return out
	}
	if len(c.buffered) > 0 {
		out = append(out, c.buffered...)
		c.buffered = c.buffered[:0]
	}

	switch r := c.rng.Float64(); {
	case r < c.p.ReorderRate:
		c.held = append(c.held, held{nowMs + 1 + c.rng.Int64N(c.p.MaxLateMs), s})
	case r < c.p.ReorderRate+c.p.DupRate:
		out = append(out, s, s)
	default:
		out = append(out, s)
	}
	return c.Flush(nowMs, out)
}

// Flush appends held (reordered) samples whose release time has come.
func (c *Channel) Flush(nowMs int64, out []telemetry.Sample) []telemetry.Sample {
	if nowMs < c.outageUntilMs {
		return out
	}
	kept := c.held[:0]
	for _, h := range c.held {
		if h.releaseMs <= nowMs {
			out = append(out, h.s)
		} else {
			kept = append(kept, h)
		}
	}
	c.held = kept
	return out
}

func (c *Channel) corrupt(s *telemetry.Sample) {
	s.TsEventMs += c.skewMs
	if c.p.GPSJitterM > 0 {
		s.Lat += c.rng.NormFloat64() * c.p.GPSJitterM / 111_200
		s.Lon += c.rng.NormFloat64() * c.p.GPSJitterM / (111_200 * math.Cos(s.Lat*math.Pi/180))
	}
	if c.rng.Float64() < c.p.DropoutRate {
		s.Dropout |= 1 << c.rng.IntN(3)
	}
	if c.rng.Float64() < c.p.MalformedRate {
		s.Malformed = telemetry.Malformed(1 + c.rng.IntN(5))
		switch s.Malformed {
		case telemetry.MalVINChecksum:
			s.VIN = BreakCheckDigit(s.VIN, c.rng.IntN(10))
		case telemetry.MalDTCFormat:
			s.DTC = []string{"PX12Z"} // wrong system digit and non-hex
		case telemetry.MalSchemaVersion:
			s.SchemaVersion = 99
		case telemetry.MalRange:
			s.SoCPct = 250
		}
	}
}

// BreakCheckDigit replaces VIN position 9 with a different valid check character, so the VIN is
// well formed but fails the checksum. k in [0,9] picks which wrong character.
func BreakCheckDigit(vin string, k int) string {
	const digits = "0123456789X"
	if len(vin) != 17 {
		return vin
	}
	i := 0
	for i < len(digits) && digits[i] != vin[8] {
		i++
	}
	b := []byte(vin)
	b[8] = digits[(i+1+k)%len(digits)]
	return string(b)
}
