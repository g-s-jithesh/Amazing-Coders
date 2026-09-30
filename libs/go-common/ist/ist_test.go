package ist

import (
	"testing"
	"time"
)

func TestMinuteOfDayAndShift(t *testing.T) {
	if m := MinuteOfDay(time.Date(2026, 1, 1, 18, 30, 0, 0, time.UTC)); m != 0 {
		t.Fatalf("18:30 UTC = 00:00 IST, got %d", m)
	}
	if !InShift(0, 1320, 360) || InShift(400, 1320, 360) || !InShift(400, 360, 840) || InShift(840, 360, 840) {
		t.Fatal("shift windows")
	}
}
