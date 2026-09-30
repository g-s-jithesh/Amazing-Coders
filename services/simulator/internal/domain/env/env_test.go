package env

import (
	"math"
	"testing"
	"time"
)

func TestMinuteOfDayIST(t *testing.T) {
	// 18:30 UTC = 00:00 IST next day; 00:00 UTC = 05:30 IST.
	if m := MinuteOfDayIST(time.Date(2026, 1, 1, 18, 30, 0, 0, time.UTC)); m != 0 {
		t.Fatalf("got %d", m)
	}
	if m := MinuteOfDayIST(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); m != 330 {
		t.Fatalf("got %d", m)
	}
}

func TestAmbientShape(t *testing.T) {
	at := func(month time.Month, hourIST int) float64 {
		return AmbientC("Chennai", time.Date(2026, month, 15, hourIST, 0, 0, 0, IST))
	}
	if !(at(5, 15) > at(5, 4)) {
		t.Error("afternoon should be hotter than pre-dawn")
	}
	if !(at(5, 15) > at(12, 15)) {
		t.Error("May should be hotter than December")
	}
	if a := at(5, 15); a < 30 || a > 40 {
		t.Errorf("Chennai May afternoon %.1f °C implausible", a)
	}
	if a := AmbientC("Nowhere", time.Date(2026, 5, 15, 15, 0, 0, 0, IST)); math.IsNaN(a) || a < 25 {
		t.Errorf("fallback climate %.1f", a)
	}
}

func TestInShift(t *testing.T) {
	cases := []struct {
		m, dep, ret int
		want        bool
	}{
		{360, 360, 840, true}, {839, 360, 840, true}, {840, 360, 840, false}, {100, 360, 840, false},
		// night shift 22:00–06:00 crosses midnight
		{1320, 1320, 360, true}, {0, 1320, 360, true}, {359, 1320, 360, true}, {360, 1320, 360, false}, {1000, 1320, 360, false},
	}
	for _, c := range cases {
		if got := InShift(c.m, c.dep, c.ret); got != c.want {
			t.Errorf("InShift(%d,%d,%d) = %v", c.m, c.dep, c.ret, got)
		}
	}
}
