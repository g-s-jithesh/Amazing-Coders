// Package env provides the simulated environment: IST clock helpers and ambient temperature.
package env

import (
	"math"
	"time"
)

// IST is UTC+05:30 with no DST (fixed zone, so it never depends on the host tz database).
var IST = time.FixedZone("IST", 5*3600+1800)

// MinuteOfDayIST returns 0..1439.
func MinuteOfDayIST(t time.Time) int {
	t = t.In(IST)
	return t.Hour()*60 + t.Minute()
}

type climate struct{ mean, diurnalAmp, seasonalAmp float64 }

// Approximate climate normals (synthetic, for realistic shape only). Peak heat is in May.
var climates = map[string]climate{
	"Bengaluru": {24, 5, 3},
	"Chennai":   {29, 4, 3},
	"Surat":     {28, 5, 5},
}

// AmbientC is a smooth deterministic ambient temperature: seasonal peak mid-May, daily peak 15:00 IST.
// Unknown cities fall back to 27 °C mean.
func AmbientC(city string, t time.Time) float64 {
	c, ok := climates[city]
	if !ok {
		c = climate{27, 5, 4}
	}
	ist := t.In(IST)
	hour := float64(ist.Hour()) + float64(ist.Minute())/60
	day := float64(ist.YearDay())
	return c.mean +
		c.seasonalAmp*math.Cos(2*math.Pi*(day-135)/365.25) +
		c.diurnalAmp*math.Cos(2*math.Pi*(hour-15)/24)
}

// InShift reports whether minute m falls in [depart, return), handling shifts that cross midnight.
func InShift(m, departMin, returnMin int) bool {
	if departMin <= returnMin {
		return m >= departMin && m < returnMin
	}
	return m >= departMin || m < returnMin
}
