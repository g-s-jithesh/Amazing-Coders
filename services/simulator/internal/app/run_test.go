package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/noise"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

type memPub struct {
	mu   sync.Mutex
	msgs map[string][]string // vin → payloads in publish order
	n    int
}

func (m *memPub) Publish(_ context.Context, _, vin string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.msgs == nil {
		m.msgs = map[string][]string{}
	}
	m.msgs[vin] = append(m.msgs[vin], string(b))
	m.n++
	return nil
}
func (m *memPub) Close() error { return nil }

type memTruth struct {
	faults []vehicle.FaultTruth
	soh    int
}

func (m *memTruth) Faults(f []vehicle.FaultTruth) error {
	m.faults = append(m.faults, f...)
	return nil
}
func (m *memTruth) SoH(s []SoHTruth) error { m.soh += len(s); return nil }

// encodeSeq encodes just what the test needs: seq and event type.
func encodeSeq(s *telemetry.Sample) ([]byte, error) {
	return []byte(s.Evt.String() + ":" + string(rune('0'+s.Seq%10))), nil
}

func TestRunCleanRatesTruthAndOrder(t *testing.T) {
	vs := fleet(t, 200)
	vs[0].InjectFault(1, start.UnixMilli(), time.Hour)
	pub, truth := &memPub{}, &memTruth{}
	var st Stats
	cfg := RunConfig{Seed: 42, Start: start, Dt: 10, RateHz: 0.01, Duration: 26 * time.Hour, Workers: 4, Noise: noise.Profiles["clean"], BurstFactor: 3, BurstFor: 5 * time.Minute}
	if err := Run(context.Background(), vs, cfg, encodeSeq, pub, truth, &st); err != nil {
		t.Fatal(err)
	}
	periodic := 200 * 26 * 3600 / 100 // one per 100 sim-seconds
	if got := int(st.Published.Load()); got < periodic || got > periodic*3/2 {
		t.Fatalf("published %d, want ≈ %d periodic (+ events + shift bursts)", got, periodic)
	}
	if st.Published.Load() != st.Samples.Load() || pub.n != int(st.Published.Load()) || st.PublishErrors.Load() != 0 {
		t.Fatalf("clean noise must publish every sample once: samples=%d published=%d", st.Samples.Load(), st.Published.Load())
	}
	if truth.soh != 2*200 { // run-start baseline + one IST midnight in 26 h from 00:00
		t.Fatalf("soh snapshots = %d rows", truth.soh)
	}
	injected := 0
	for _, f := range truth.faults {
		if f.Injected && f.VIN == vs[0].VIN {
			injected++
		}
	}
	if injected != 1 { // InjectFault buffers the record on the vehicle; Run drains it on the first tick
		t.Fatalf("injected fault records = %d, want 1", injected)
	}
	if st.Ticks.Load() != int64(26*3600/10) {
		t.Fatalf("ticks = %d", st.Ticks.Load())
	}
}

func TestRunBurstAfterIgnitionOn(t *testing.T) {
	vs := fleet(t, 50)
	pub := &memPub{}
	var st Stats
	// Rate 1/300 s; a burst factor of 3 for 1 h after IGN_ON should raise the count well above base.
	run := func(factor int) int64 {
		st = Stats{}
		vs = fleet(t, 50)
		cfg := RunConfig{Seed: 42, Start: start, Dt: 10, RateHz: 1.0 / 300, Duration: 24 * time.Hour, Workers: 2, Noise: noise.Profiles["clean"], BurstFactor: factor, BurstFor: time.Hour}
		if err := Run(context.Background(), vs, cfg, encodeSeq, pub, nil, &st); err != nil {
			t.Fatal(err)
		}
		return st.Published.Load()
	}
	base, burst := run(1), run(3)
	if burst < base+50*8 { // each vehicle: ≥ 1 departure/day × 1 h at 3× → ≥ 24 extra, be conservative
		t.Fatalf("burst run published %d vs base %d", burst, base)
	}
}

func TestRunStopsOnCancelAndRejectsBadConfig(t *testing.T) {
	vs := fleet(t, 10)
	ctx, cancel := context.WithCancel(context.Background())
	var st Stats
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	cfg := RunConfig{Start: start, Dt: 1, RateHz: 1, Speedup: 1, Noise: noise.Profiles["clean"]} // real-time: would run forever
	begin := time.Now()
	if err := Run(ctx, vs, cfg, encodeSeq, &memPub{}, nil, &st); err != nil || time.Since(begin) > 2*time.Second {
		t.Fatalf("err=%v took %v", err, time.Since(begin))
	}
	if st.Ticks.Load() < 1 || st.Ticks.Load() > 5 {
		t.Fatalf("speedup 1 ran %d ticks in ~50 ms", st.Ticks.Load())
	}
	if err := Run(context.Background(), vs, RunConfig{Dt: 0, RateHz: 1}, encodeSeq, &memPub{}, nil, &st); err == nil {
		t.Fatal("dt 0 must be rejected")
	}
}

func TestRunWithNoiseIsDeterministic(t *testing.T) {
	once := func() map[string][]string {
		pub := &memPub{}
		var st Stats
		cfg := RunConfig{Seed: 42, Start: start, Dt: 5, RateHz: 0.2, Duration: 6 * time.Hour, Workers: 3, Noise: noise.Profiles["hostile"], BurstFactor: 3, BurstFor: 5 * time.Minute}
		if err := Run(context.Background(), fleet(t, 30), cfg, encodeSeq, pub, nil, &st); err != nil {
			t.Fatal(err)
		}
		return pub.msgs
	}
	a, b := once(), once()
	for vin, msgs := range a {
		if len(b[vin]) != len(msgs) {
			t.Fatalf("%s: %d vs %d messages", vin, len(msgs), len(b[vin]))
		}
		for i := range msgs {
			if msgs[i] != b[vin][i] {
				t.Fatalf("%s message %d differs", vin, i)
			}
		}
	}
}
