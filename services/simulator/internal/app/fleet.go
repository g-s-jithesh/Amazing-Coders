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

// VehicleRNG seeds a vehicle's private RNG from hash(seed, VIN), so its trajectory does not depend
// on which shard or goroutine simulates it.
func VehicleRNG(seed uint64, vin string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(vin))
	return rand.New(rand.NewPCG(seed, h.Sum64()))
}

// BuildFleet creates live vehicles for the dataset at sim start time `start`. Packs are pre-aged
// from commissioning to start with the closed-form ageing model and each pack's quality factor.
func BuildFleet(ds *masterdata.Dataset, models []masterdata.Model, seed uint64, start time.Time) ([]*vehicle.Vehicle, error) {
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

		out[i] = vehicle.New(vehicle.Spec{
			VIN: mv.VIN, OEM: mv.OEM, City: dep.City,
			WhPerKm: float64(m.WhPerKm), MaxACKW: m.MaxACKW,
			HomeLat: dep.Lat, HomeLon: dep.Lon,
			CityLat: city.Lat, CityLon: city.Lon, CitySpanDeg: city.SpanDeg,
			DepartMin: duty.DepartMin, ReturnMin: duty.ReturnMin, PlannedKm: float64(duty.PlannedKm),
			IsolationBaseKohm: 1500 + 2500*rng.Float64(),
		}, pack, batt, rng)
	}
	return out, nil
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
