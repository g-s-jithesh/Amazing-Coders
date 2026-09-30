package sketch

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// Zipf-like stream: CMS never under-counts, over-counts by ≤ ε·N for (almost) all keys,
// and top-K returns the true heaviest keys.
func TestCMSBoundAndTopK(t *testing.T) {
	const eps, delta = 0.001, 0.01
	tk := NewTopK(5, eps, delta)
	r := rand.New(rand.NewPCG(7, 8))
	z := rand.NewZipf(r, 1.3, 1, 2000)
	truth := map[string]uint32{}
	for i := 0; i < 200_000; i++ {
		k := fmt.Sprintf("P%04d", z.Uint64())
		truth[k]++
		tk.Add(k)
	}
	bound := uint32(eps * float64(tk.CMS.N))
	over := 0
	for k, c := range truth {
		est := tk.CMS.Estimate(k)
		if est < c {
			t.Fatalf("under-count for %s: %d < %d", k, est, c)
		}
		if est-c > bound {
			over++
		}
	}
	if frac := float64(over) / float64(len(truth)); frac > delta {
		t.Fatalf("%.4f of keys exceed ε·N (allowed δ=%.2f)", frac, delta)
	}
	top := tk.Top()
	for i, want := range []string{"P0000", "P0001", "P0002", "P0003", "P0004"} {
		if top[i].Key != want {
			t.Fatalf("top[%d] = %s, want %s (top=%v)", i, top[i].Key, want, top)
		}
	}
}

func TestTopKSmallStream(t *testing.T) {
	tk := NewTopK(2, 0.01, 0.01)
	for _, k := range []string{"A", "B", "A", "C", "A", "B"} {
		tk.Add(k)
	}
	if got := tk.Top(); len(got) != 2 || got[0].Key != "A" || got[0].Count != 3 || got[1].Key != "B" {
		t.Fatalf("top = %v", got)
	}
}
