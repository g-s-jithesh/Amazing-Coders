// Package geo holds pure geographic helpers.
package geo

const base32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// Geohash encodes lat/lon to the given precision (precision 6 ≈ 1.2 km × 0.6 km). O(precision).
func Geohash(lat, lon float64, precision int) string {
	latR, lonR := [2]float64{-90, 90}, [2]float64{-180, 180}
	out := make([]byte, 0, precision)
	even, bit, ch := true, 0, 0
	for len(out) < precision {
		r, x := &latR, lat
		if even {
			r, x = &lonR, lon
		}
		mid := (r[0] + r[1]) / 2
		ch <<= 1
		if x >= mid {
			ch |= 1
			r[0] = mid
		} else {
			r[1] = mid
		}
		even = !even
		if bit++; bit == 5 {
			out = append(out, base32[ch])
			bit, ch = 0, 0
		}
	}
	return string(out)
}
