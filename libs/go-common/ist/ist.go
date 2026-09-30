// Package ist holds India Standard Time helpers shared by the simulator and stream-processor.
// IST is UTC+05:30 with no DST, so a fixed zone is exact and never depends on the host tz database.
package ist

import "time"

var Zone = time.FixedZone("IST", 5*3600+1800)

// MinuteOfDay returns the IST minute of day, 0..1439.
func MinuteOfDay(t time.Time) int {
	t = t.In(Zone)
	return t.Hour()*60 + t.Minute()
}

// InShift reports whether minute m is in [depart, return); shifts may cross midnight (return < depart).
func InShift(m, departMin, returnMin int) bool {
	if departMin <= returnMin {
		return m >= departMin && m < returnMin
	}
	return m >= departMin || m < returnMin
}
