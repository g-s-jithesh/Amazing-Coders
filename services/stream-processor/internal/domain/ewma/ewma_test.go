package ewma

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestConvergesAndScores(t *testing.T) {
	e := EWMA{Alpha: 0.01}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20_000; i++ {
		e.Update(10 + r.NormFloat64()*2)
	}
	if math.Abs(e.Mean-10) > 0.5 || math.Abs(math.Sqrt(e.Var)-2) > 0.4 {
		t.Fatalf("mean %.2f sd %.2f, want ≈10 / ≈2", e.Mean, math.Sqrt(e.Var))
	}
	if z := e.Z(18, 100); z < 3 || z > 5 {
		t.Fatalf("z(18) = %.2f, want ≈4", z)
	}
	if (&EWMA{Alpha: 0.1}).Z(100, 5) != 0 {
		t.Fatal("z must be 0 before min samples")
	}
	flat := EWMA{Alpha: 0.1}
	for i := 0; i < 10; i++ {
		flat.Update(5)
	}
	if z := flat.Z(5.001, 1); math.IsInf(z, 0) || math.IsNaN(z) || z <= 0 {
		t.Fatalf("zero-variance baseline must give a finite positive z, got %v", z)
	}
}
