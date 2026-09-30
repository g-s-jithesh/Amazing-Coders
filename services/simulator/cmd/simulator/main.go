// Command simulator generates the synthetic Kilowatt EV fleet.
//
//	simulator seed --seed 42 --vehicles 100000 --tenants 3 --ref data/reference --out data/seed
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/refdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/seed"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: simulator seed [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = runSeed(os.Args[2:])
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
