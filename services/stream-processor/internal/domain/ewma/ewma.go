// Package ewma is an exponentially weighted mean/variance with z-scores (West's incremental form). O(1).
package ewma

import "math"

type EWMA struct {
	Alpha     float64 // weight of the newest sample, 0 < α ≤ 1
	Mean, Var float64
	N         int
}

// Z is the z-score of x against the current baseline (0 until MinN samples; +Inf-safe for Var = 0).
func (e *EWMA) Z(x float64, minN int) float64 {
	if e.N < minN {
		return 0
	}
	sd := math.Sqrt(e.Var)
	if sd < 1e-9 {
		sd = 1e-9
	}
	return (x - e.Mean) / sd
}

// Update folds x into the baseline.
func (e *EWMA) Update(x float64) {
	if e.N == 0 {
		e.Mean, e.N = x, 1
		return
	}
	d := x - e.Mean
	e.Mean += e.Alpha * d
	e.Var = (1 - e.Alpha) * (e.Var + e.Alpha*d*d)
	e.N++
}
