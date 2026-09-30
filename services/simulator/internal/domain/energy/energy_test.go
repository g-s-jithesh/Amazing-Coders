package energy

import (
	"math"
	"testing"
)

func TestTractionW(t *testing.T) {
	if TractionW(150, 0) != 0 || TractionW(150, -5) != 0 {
		t.Fatal("stationary vehicle must draw no traction power")
	}
	if got := TractionW(150, 40); math.Abs(got-6000) > 1e-9 { // rated Wh/km at reference speed
		t.Fatalf("40 km/h: %.1f W", got)
	}
	whPerKm := func(v float64) float64 { return TractionW(150, v) / v }
	if !(whPerKm(80) > whPerKm(40) && whPerKm(40) > whPerKm(20)) {
		t.Fatal("Wh/km should rise with speed")
	}
}

func TestHVACW(t *testing.T) {
	for amb, want := range map[float64]float64{22: 0, 32: 1500, 12: 1500, 60: 3000} {
		if got := HVACW(amb); got != want {
			t.Errorf("HVACW(%v) = %v, want %v", amb, got, want)
		}
	}
}
