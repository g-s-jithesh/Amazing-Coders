// Command simulator generates the synthetic Kilowatt EV fleet.
//
//	simulator seed  --seed 42 --vehicles 100000 --tenants 3 --ref data/reference --out data/seed
//	simulator trace --seed 42 --vin <VIN> --start 2026-09-01T00:00:00+05:30 --hours 48 --every 60 > trace.csv
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/refdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/seed"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/env"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/fault"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: simulator seed|trace [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = runSeed(os.Args[2:])
	case "trace":
		err = runTrace(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		slog.Error("simulator failed", "err", err)
		os.Exit(1)
	}
}

func runSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	seedV := fs.Uint64("seed", 42, "RNG seed (output is deterministic for a given seed/vehicles/tenants)")
	vehicles := fs.Int("vehicles", 100_000, "number of vehicles")
	tenants := fs.Int("tenants", 3, "number of tenants")
	ref := fs.String("ref", "data/reference", "reference data directory")
	out := fs.String("out", "data/seed", "output directory for CSVs")
	_ = fs.Parse(args)

	models, err := refdata.Models(*ref)
	if err != nil {
		return err
	}
	wmis, err := refdata.WMIs(*ref)
	if err != nil {
		return err
	}
	start := time.Now()
	ds, err := masterdata.Generate(masterdata.Config{Seed: *seedV, Vehicles: *vehicles, Tenants: *tenants, Models: models, WMI: wmis})
	if err != nil {
		return err
	}
	if err := seed.Write(*out, ds, models); err != nil {
		return err
	}
	slog.Info("seed written", "out", *out, "tenants", len(ds.Tenants), "fleets", len(ds.Fleets), "depots", len(ds.Depots),
		"vehicles", len(ds.Vehicles), "sites", len(ds.Sites), "chargers", len(ds.Chargers), "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// runTrace simulates one vehicle (from the deterministic master data) and prints CSV to stdout.
// It includes soh_true, the simulator's ground truth: a developer diagnostic, never pipeline input.
func runTrace(args []string) error {
	fs := flag.NewFlagSet("trace", flag.ExitOnError)
	seedV := fs.Uint64("seed", 42, "RNG seed")
	vehicles := fs.Int("vehicles", 100_000, "fleet size used to generate master data (must match `seed`)")
	tenants := fs.Int("tenants", 3, "number of tenants (must match `seed`)")
	ref := fs.String("ref", "data/reference", "reference data directory")
	vinFlag := fs.String("vin", "", "VIN to trace (default: first vehicle)")
	startS := fs.String("start", "2026-09-01T00:00:00+05:30", "sim start (RFC 3339)")
	hours := fs.Float64("hours", 48, "duration")
	dt := fs.Float64("dt", 1, "step seconds")
	every := fs.Int("every", 60, "emit one row every N steps")
	faultRate := fs.Float64("fault-rate", 0.5, "random fault onsets per vehicle-year (at SoH 100 %)")
	inject := fs.String("inject", "", "start this fault at sim start (e.g. cooling_degradation)")
	precursor := fs.Duration("precursor", 48*time.Hour, "precursor length for --inject")
	_ = fs.Parse(args)

	start, err := time.Parse(time.RFC3339, *startS)
	if err != nil {
		return err
	}
	models, err := refdata.Models(*ref)
	if err != nil {
		return err
	}
	wmis, err := refdata.WMIs(*ref)
	if err != nil {
		return err
	}
	ds, err := masterdata.Generate(masterdata.Config{Seed: *seedV, Vehicles: *vehicles, Tenants: *tenants, Models: models, WMI: wmis})
	if err != nil {
		return err
	}
	idx := 0
	if *vinFlag != "" {
		idx = -1
		for i, v := range ds.Vehicles {
			if v.VIN == *vinFlag {
				idx = i
				break
			}
		}
		if idx < 0 {
			return fmt.Errorf("vin %s not in master data for seed=%d vehicles=%d", *vinFlag, *seedV, *vehicles)
		}
	}
	one := &masterdata.Dataset{Depots: ds.Depots, Vehicles: ds.Vehicles[idx : idx+1], Duties: ds.Duties[idx : idx+1]}
	vs, err := app.BuildFleet(one, models, *seedV, start, app.Options{FaultRatePerYear: *faultRate})
	if err != nil {
		return err
	}
	v := vs[0]
	if *inject != "" {
		k, err := fault.Parse(*inject)
		if err != nil {
			return err
		}
		v.InjectFault(k, start.UnixMilli(), *precursor)
	}
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	fmt.Fprintln(w, "ts_ist,mode,evt,lat,lon,speed_kmh,odo_km,soc_pct,pack_voltage_v,pack_current_a,pack_temp_max_c,cell_v_min_mv,cell_v_max_mv,isolation_kohm,hv_interlock_ok,aux_12v_v,ambient_c,charge_power_kw,dtc,soh_true")
	for i := 0; i < int(*hours*3600 / *dt); i++ {
		now := start.Add(time.Duration(float64(i) * *dt * float64(time.Second)))
		app.StepAll(vs, now, *dt, 1)
		for periodic := i%*every == 0; periodic || v.PendingEvents() > 0; periodic = false {
			s := v.Sample(now.UnixMilli())
			fmt.Fprintf(w, "%s,%s,%d,%.6f,%.6f,%.1f,%.3f,%.2f,%.1f,%.1f,%.1f,%d,%d,%.0f,%t,%.2f,%.1f,%.2f,%s,%.5f\n",
				now.In(env.IST).Format("2006-01-02T15:04:05"), v.Mode, s.Evt, s.Lat, s.Lon, s.SpeedKmh, s.OdoKm, s.SoCPct,
				s.PackVoltageV, s.PackCurrentA, s.PackTempMaxC, s.CellVMinMv, s.CellVMaxMv, s.IsolationKohm, s.HVInterlockOK, s.Aux12vV,
				s.AmbientC, s.ChargePowerKW, strings.Join(s.DTC, "|"), v.Batt.SoH())
		}
	}
	return nil
}
