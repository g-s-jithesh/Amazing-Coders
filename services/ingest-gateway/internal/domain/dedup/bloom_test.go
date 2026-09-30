package dedup

import (
	"fmt"
	"math"
	"testing"
)

func TestSizing(t *testing.T) {
	r := NewRotating(1_000_000, 0.01, 600_000, 0)
	// m/n ≈ 9.585 bits per key and k ≈ 7 for p = 1 %.
	if bpk := float64(r.M()) / 1e6; math.Abs(bpk-9.585) > 0.01 || r.K() != 7 {
		t.Fatalf("bits/key %.3f k %d", bpk, r.K())
	}
}

func TestNoFalseNegativesWithinWindow(t *testing.T) {
	r := NewRotating(50_000, 0.01, 600_000, 0)
	for i := 0; i < 50_000; i++ {
		r.SeenOrAdd("0KCDV45N9RC000001", uint64(i), int64(i)) // well inside the first half-window
	}
	for i := 0; i < 50_000; i++ {
		if !r.SeenOrAdd("0KCDV45N9RC000001", uint64(i), 400_000) { // after one rotation: still in prev
			t.Fatalf("false negative for seq %d", i)
		}
	}
}

func TestFalsePositiveRateNearDesign(t *testing.T) {
	const n = 100_000
	r := NewRotating(n, 0.01, 600_000, 0)
	for i := 0; i < n; i++ {
		r.SeenOrAdd(fmt.Sprintf("VIN%014d", i), uint64(i), 0)
	}
	fp := 0
	for i := 0; i < n; i++ {
		if r.mayContain(fmt.Sprintf("NEW%014d", i), uint64(i)) {
			fp++
		}
	}
	// Designed for 1 % at n keys; the binomial σ at n = 100K is ~0.03 pp, so 1.2 % is a generous bound.
	rate := float64(fp) / n
	t.Logf("measured false-positive rate %.4f (design 0.0100)", rate)
	if rate > 0.012 {
		t.Fatalf("false-positive rate %.4f", rate)
	}
}

func TestForgetsAfterTwoHalfWindows(t *testing.T) {
	r := NewRotating(1000, 0.01, 600_000, 0)
	r.SeenOrAdd("V", 1, 0)
	if !r.SeenOrAdd("V", 1, 299_999) {
		t.Fatal("forgot inside the first half-window")
	}
	if r.SeenOrAdd("V", 2, 900_000) || r.SeenOrAdd("V", 1, 1_200_000) {
		t.Fatal("key must be forgotten once it is older than the full window")
	}
}

func BenchmarkSeenOrAdd(b *testing.B) {
	r := NewRotating(1_000_000, 0.01, 600_000, 0)
	for i := 0; i < b.N; i++ {
		r.SeenOrAdd("0KCDV45N9RC000001", uint64(i), int64(i))
	}
}
