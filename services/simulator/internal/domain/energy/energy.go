// Package energy converts driving conditions to battery power demand. O(1).
package energy

import "math"

// Reference speed at which a model's rated Wh/km applies (urban duty cycle, regen included).
const refSpeedKmh = 40.0

// TractionW is the traction power at speed: rated Wh/km scaled by an aero/rolling factor
// (1.0 at 40 km/h, 1.6 at 80 km/h), times speed.
func TractionW(whPerKm, speedKmh float64) float64 {
	if speedKmh <= 0 {
		return 0
	}
	f := 0.8 + 0.2*(speedKmh/refSpeedKmh)*(speedKmh/refSpeedKmh)
	return whPerKm * f * speedKmh
}

// HVACW is cabin/pack conditioning load: 150 W per °C away from 22 °C, capped at 3 kW.
func HVACW(ambientC float64) float64 {
	return math.Min(3000, 150*math.Abs(ambientC-22))
}

// AuxW is the constant low-voltage load (DC-DC, controllers, telematics) while awake.
const AuxW = 300.0
