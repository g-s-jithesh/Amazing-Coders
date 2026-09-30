package app

import (
	"crypto/sha256"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

var (
	start  = time.Date(2026, 9, 1, 0, 0, 0, 0, env.IST)
	models = []masterdata.Model{
		{Code: "CV21L", OEM: "oem_a", Chemistry: "LFP", CapacityKWh: 21, VoltageV: 320, MaxACKW: 3.3, WhPerKm: 140, DailyKmMin: 80, DailyKmMax: 140, Share: 0.4},
		{Code: "SD30L", OEM: "oem_b", Chemistry: "LFP", CapacityKWh: 30, VoltageV: 320, MaxACKW: 7.2, WhPerKm: 130, DailyKmMin: 120, DailyKmMax: 220, Share: 0.3},
		{Code: "DV45N", OEM: "oem_c", Chemistry: "NMC", CapacityKWh: 45, VoltageV: 350, MaxACKW: 11, WhPerKm: 190, DailyKmMin: 90, DailyKmMax: 160, Share: 0.3},
	}
	wmis = map[string]string{"oem_a": "0KA", "oem_b": "0KB", "oem_c": "0KC"}
)

func fleet(t testing.TB, n int) []*vehicle.Vehicle {
	t.Helper()
	ds, err := masterdata.Generate(masterdata.Config{Seed: 42, Vehicles: n, Tenants: 2, Models: models, WMI: wmis})
	if err != nil {
		t.Fatal(err)
	}
	vs, err := BuildFleet(ds, models, 42, start)
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

// runHashes runs vs for `hours` at dt and returns a per-VIN SHA-256 over all samples.
func runHashes(vs []*vehicle.Vehicle, hours, dt float64, workers int) map[string]string {
	h := map[string][]byte{}
	for i := 0; i < int(hours*3600/dt); i++ {
		now := start.Add(time.Duration(float64(i) * dt * float64(time.Second)))
		StepAll(vs, now, dt, workers)
		for _, v := range vs {
			h[v.VIN] = fmt.Appendf(h[v.VIN], "%+v", v.Sample(now.UnixMilli()))
		}
	}
	out := map[string]string{}
	for vin, b := range h {
		out[vin] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return out
}

// CLAUDE.md simulator invariant 1: same seed → same per-vehicle output for shards=1 and shards=4.
func TestDeterministicAcrossShards(t *testing.T) {
	whole := runHashes(fleet(t, 40), 8, 5, 1)

	all := fleet(t, 40)
	sharded := map[string]string{}
	for k := 0; k < 4; k++ {
		var part []*vehicle.Vehicle
		for i, v := range all {
			if i%4 == k {
				part = append(part, v)
			}
		}
		for vin, h := range runHashes(part, 8, 5, 3) {
			sharded[vin] = h
		}
	}
	if len(whole) != 40 || len(sharded) != 40 {
		t.Fatalf("hash counts %d/%d", len(whole), len(sharded))
	}
	for vin, h := range whole {
		if sharded[vin] != h {
			t.Fatalf("vehicle %s differs between 1 shard and 4 shards", vin)
		}
	}
}

func TestPreAgedFleetIsPlausible(t *testing.T) {
	vs := fleet(t, 2000)
	lo, hi, sum := 1.0, 0.0, 0.0
	for _, v := range vs {
		soh := v.Batt.SoH()
		lo, hi, sum = math.Min(lo, soh), math.Max(hi, soh), sum+soh
	}
	mean := sum / float64(len(vs))
	// Vehicles are 0.2–5.7 years old at start: expect a real spread, all well above end-of-life.
	if lo < 0.70 || hi > 0.999 || mean < 0.85 || mean > 0.97 || hi-lo < 0.05 {
		t.Fatalf("SoH min=%.3f max=%.3f mean=%.3f", lo, hi, mean)
	}
}

// Three simulated days for 300 vehicles: physical bounds, SoH never rises, and the fleet
// actually drives and charges.
func TestFleetInvariantsOverThreeDays(t *testing.T) {
	vs := fleet(t, 300)
	prevSoH := make([]float64, len(vs))
	for i, v := range vs {
		prevSoH[i] = v.Batt.SoH()
	}
	drove, charged := map[string]bool{}, map[string]bool{}
	const dt = 10.0
	for i := 0; i < int(72*3600/dt); i++ {
		now := start.Add(time.Duration(float64(i) * dt * float64(time.Second)))
		StepAll(vs, now, dt, 4)
		for k, v := range vs {
			s := v.Sample(now.UnixMilli())
			if math.IsNaN(float64(s.PackVoltageV)) || s.SoCPct < 0 || s.SoCPct > 100 || s.PackTempMaxC > 70 || s.PackTempMinC < -5 {
				t.Fatalf("%s out of bounds: %+v", v.VIN, s)
			}
			if s.CellVMaxMv <= s.CellVMinMv {
				t.Fatalf("%s cell max %d ≤ min %d", v.VIN, s.CellVMaxMv, s.CellVMinMv)
			}
			if soh := v.Batt.SoH(); soh > prevSoH[k] {
				t.Fatalf("%s SoH rose %.8f → %.8f", v.VIN, prevSoH[k], soh)
			} else {
				prevSoH[k] = soh
			}
			if s.SpeedKmh > 0 {
				drove[v.VIN] = true
			}
			if s.ChargeState == telemetry.ChargeAC {
				charged[v.VIN] = true
			}
		}
	}
	if len(drove) < 290 || len(charged) < 290 {
		t.Fatalf("drove=%d charged=%d of 300", len(drove), len(charged))
	}
}

func BenchmarkStepAll100K(b *testing.B) {
	vs := fleet(b, 100_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StepAll(vs, start.Add(time.Duration(i)*time.Second), 1, 8)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/100_000, "ns/vehicle")
}
