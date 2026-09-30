// Package app wires master data into live vehicles and advances them on a shared clock.
package app

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/battery"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

// Options tune the fleet build.
type Options struct {
	FaultRatePerYear float64 // random fault onsets per vehicle-year at SoH 100 % (0 disables)
}

// VehicleRNG seeds a vehicle's private RNG from hash(seed, VIN), so its trajectory does not depend
// on which shard or goroutine simulates it.
func VehicleRNG(seed uint64, vin string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(vin))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}

// NoiseRNG is a second per-vehicle stream for the noise layer, so enabling or changing noise never
// alters a vehicle's physical trajectory.
func NoiseRNG(seed uint64, vin string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(vin))
	return rand.New(rand.NewPCG(seed^0x6e6f697365, h.Sum64())) // "noise"
}

// BuildFleet creates live vehicles for the dataset at sim start time `start`. Packs are pre-aged
// from commissioning to start with the closed-form ageing model and each pack's quality factor.
func BuildFleet(ds *masterdata.Dataset, models []masterdata.Model, seed uint64, start time.Time, opts Options) ([]*vehicle.Vehicle, error) {
	modelByCode := map[string]masterdata.Model{}
	for _, m := range models {
		modelByCode[m.Code] = m
	}
	depotByID := map[string]masterdata.Depot{}
	for _, d := range ds.Depots {
		depotByID[d.ID] = d
	}
	cityByName := map[string]masterdata.City{}
	for _, c := range masterdata.Cities {
		cityByName[c.Name] = c
	}
	if len(ds.Duties) != len(ds.Vehicles) {
		return nil, fmt.Errorf("app: %d duties for %d vehicles", len(ds.Duties), len(ds.Vehicles))
	}

	out := make([]*vehicle.Vehicle, len(ds.Vehicles))
	for i, mv := range ds.Vehicles {
		m, ok := modelByCode[mv.ModelCode]
		if !ok {
			return nil, fmt.Errorf("app: vehicle %s has unknown model %s", mv.VIN, mv.ModelCode)
		}
		dep := depotByID[mv.HomeDepotID]
		city := cityByName[dep.City]
		duty := ds.Duties[i]
		rng := VehicleRNG(seed, mv.VIN)

		quality := math.Max(0.8, math.Min(1.3, math.Exp(0.12*rng.NormFloat64())))
		pack := battery.NewParams(battery.Chemistry(m.Chemistry), m.CapacityKWh, m.VoltageV, quality)
		batt := battery.State{SoC: 0.4 + 0.5*rng.Float64(), CoolingEff: 1}
		batt.TempC = env.AmbientC(dep.City, start)
		if years := start.Sub(mv.CommissionedOn).Hours() / 24 / 365.25; years > 0 {
			dailyEFC := float64(duty.PlannedKm*m.WhPerKm) / 1000 / m.CapacityKWh
			pack.PreAge(&batt, years, env.AmbientC(dep.City, start)+3, 0.65, years*365.25*dailyEFC)
		}

		v := vehicle.New(vehicle.Spec{
			VIN: mv.VIN, OEM: mv.OEM, City: dep.City,
			WhPerKm: float64(m.WhPerKm), MaxACKW: m.MaxACKW,
			HomeLat: dep.Lat, HomeLon: dep.Lon,
			CityLat: city.Lat, CityLon: city.Lon, CitySpanDeg: city.SpanDeg,
			DepartMin: duty.DepartMin, ReturnMin: duty.ReturnMin, PlannedKm: float64(duty.PlannedKm),
			IsolationBaseKohm: 1500 + 2500*rng.Float64(),
			FaultRatePerYear:  opts.FaultRatePerYear,
		}, pack, batt, rng)
		v.ScheduleNextFault(start.UnixMilli())
		v.StartMidShift(TickAt(start, 1))
		out[i] = v
	}
	return out, nil
}

// SoHTruth is one pack's true state at a point in time (ground-truth sink only).
type SoHTruth struct {
	VIN     string
	AsOfMs  int64
	SoHPct  float64
	QCalPct float64
	QCycPct float64
	EFC     float64
}

// SnapshotSoH records every vehicle's true SoH; call once per simulated day.
func SnapshotSoH(vs []*vehicle.Vehicle, now time.Time) []SoHTruth {
	out := make([]SoHTruth, len(vs))
	for i, v := range vs {
		b := v.Batt
		out[i] = SoHTruth{v.VIN, now.UnixMilli(), b.SoH() * 100, b.QCal * 100, b.QCyc * 100, b.EFC}
	}
	return out
}

// DrainFaultTruth collects fault records produced since the last call.
func DrainFaultTruth(vs []*vehicle.Vehicle) []vehicle.FaultTruth {
	var out []vehicle.FaultTruth
	for _, v := range vs {
		out = append(out, v.DrainTruth()...)
	}
	return out
}

// TickAt builds the shared per-tick clock.
func TickAt(now time.Time, dt float64) vehicle.Tick {
	ist := now.In(env.IST)
	y, m, d := ist.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
	return vehicle.Tick{NowMs: now.UnixMilli(), Dt: dt, MinIST: env.MinuteOfDayIST(now), DayIST: day}
}

// StepAll advances every vehicle by one tick using `workers` goroutines over contiguous slices.
// Ambient temperature is computed once per city per tick.
func StepAll(vs []*vehicle.Vehicle, now time.Time, dt float64, workers int) {
	tk := TickAt(now, dt)
	amb := map[string]float64{}
	for _, c := range masterdata.Cities {
		amb[c.Name] = env.AmbientC(c.Name, now)
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	chunk := (len(vs) + workers - 1) / workers
	for lo := 0; lo < len(vs); lo += chunk {
		hi := min(lo+chunk, len(vs))
		wg.Add(1)
		go func(part []*vehicle.Vehicle) {
			defer wg.Done()
			for _, v := range part {
				v.Step(tk, amb[v.City])
			}
		}(vs[lo:hi])
	}
	wg.Wait()
}
