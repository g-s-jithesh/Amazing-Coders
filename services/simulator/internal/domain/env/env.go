// Package env provides the simulated environment: IST clock helpers and ambient temperature.
package env

import (
	"math"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/ist"
)

// IST is UTC+05:30 (shared with stream-processor via libs/go-common/ist).
var IST = ist.Zone

// MinuteOfDayIST returns 0..1439.
func MinuteOfDayIST(t time.Time) int { return ist.MinuteOfDay(t) }

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
	local := t.In(IST)
	hour := float64(local.Hour()) + float64(local.Minute())/60
	day := float64(local.YearDay())
	return c.mean +
		c.seasonalAmp*math.Cos(2*math.Pi*(day-135)/365.25) +
		c.diurnalAmp*math.Cos(2*math.Pi*(hour-15)/24)
}

// InShift reports whether minute m falls in [depart, return), handling shifts that cross midnight.
func InShift(m, departMin, returnMin int) bool { return ist.InShift(m, departMin, returnMin) }
