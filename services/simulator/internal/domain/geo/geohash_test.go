package geo

import "testing"

func TestGeohash(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		p        int
		want     string
	}{
		{57.64911, 10.40744, 11, "u4pruydqqvj"}, // reference value from geohash.org / Wikipedia
		{0, 0, 1, "s"},
		{-90, -180, 3, "000"},
		{90, 180, 3, "zzz"},
	} {
		if got := Geohash(c.lat, c.lon, c.p); got != c.want {
			t.Errorf("Geohash(%v,%v,%d) = %q, want %q", c.lat, c.lon, c.p, got, c.want)
		}
	}
}
