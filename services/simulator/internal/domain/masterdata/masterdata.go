// Package masterdata deterministically generates the synthetic fleet master data
// (tenants, fleets, depots, charger sites, chargers, packs, vehicles, drivers, duties).
// Same Config → byte-identical output. O(vehicles).
package masterdata

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/geo"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vin"
)

type Model struct {
	Code, Name, OEM, Chemistry      string
	CapacityKWh, VoltageV, MaxACKW  float64
	MaxDCKW                         float64
	WhPerKm, DailyKmMin, DailyKmMax int
	Share                           float64
}

type Config struct {
	Seed     uint64
	Vehicles int
	Tenants  int
	Models   []Model
	WMI      map[string]string // oem → WMI
}

// City bounding boxes are approximate urban areas; coordinates are synthetic points inside them.
type City struct {
	Name     string
	Plant    byte // VIN position 11
	Lat, Lon float64
	SpanDeg  float64
}

var Cities = []City{
	{"Bengaluru", 'B', 12.9716, 77.5946, 0.12},
	{"Chennai", 'C', 13.0827, 80.2707, 0.10},
	{"Surat", 'S', 21.1702, 72.8311, 0.08},
}

const (
	VehiclesPerDepot   = 250
	ChargersPerVeh     = 0.5 // depot charger ratio: fewer plugs than vehicles, so dispatch matters
	SiteCapFactor      = 0.5 // site cap / sum(charger max_kw): the cap binds if everyone plugs in at once
	PublicSitesPerCity = 150
)

type Shift struct {
	Name                 string
	DepartMin, ReturnMin int // minutes of day, IST
	Weight               float64
}

// Night shift crosses midnight on purpose (edge case for the optimiser).
var Shifts = []Shift{
	{"morning", 6 * 60, 14 * 60, 0.35},
	{"general", 9 * 60, 18 * 60, 0.30},
	{"evening", 14 * 60, 22 * 60, 0.25},
	{"night", 22 * 60, 6 * 60, 0.10},
}

type (
	Tenant struct{ ID, Name string }
	Fleet  struct{ ID, TenantID, Name, City string }
	Depot  struct {
		ID, TenantID, FleetID, Name, City string
		Lat, Lon                          float64
	}
	ChargerSite struct {
		ID, TenantID, DepotID, Kind, Name, City, Geohash6 string // TenantID/DepotID empty for PUBLIC
		Lat, Lon, PowerCapKW                              float64
	}
	Charger struct {
		ID, TenantID, SiteID, OCPPID, ConnectorType string
		MaxKW                                       float64
	}
	Pack struct {
		ID, TenantID, Serial, ModelCode string
		InstallDate                     time.Time
	}
	Vehicle struct {
		ID, TenantID, VIN, FleetID, ModelCode, PackID, OEM, HomeDepotID string
		CommissionedOn                                                  time.Time
	}
	Driver struct{ ID, TenantID, Ref, DepotID string }
	Duty   struct {
		VehicleID, TenantID, DepotID, Shift string
		DepartMin, ReturnMin, PlannedKm     int
		RequiredSoCPct                      int
	}
)

type Dataset struct {
	Tenants  []Tenant
	Fleets   []Fleet
	Depots   []Depot
	Sites    []ChargerSite
	Chargers []Charger
	Packs    []Pack
	Vehicles []Vehicle
	Drivers  []Driver
	Duties   []Duty
}

var (
	commissionFrom = time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	commissionTo   = time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
)

func uuid(r *rand.Rand) string {
	hi, lo := r.Uint64(), r.Uint64()
	hi = hi&^0xF000 | 0x4000     // version 4
	lo = lo&^(0xC<<60) | 0x8<<60 // RFC 4122 variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", hi>>32, hi>>16&0xFFFF, hi&0xFFFF, lo>>48, lo&0xFFFFFFFFFFFF)
}

func pickWeighted[T any](r *rand.Rand, items []T, w func(T) float64) T {
	total := 0.0
	for _, it := range items {
		total += w(it)
	}
	x := r.Float64() * total
	for _, it := range items {
		if x -= w(it); x < 0 {
			return it
		}
	}
	return items[len(items)-1]
}

func jitter(r *rand.Rand, c City) (float64, float64) {
	return c.Lat + (r.Float64()*2-1)*c.SpanDeg, c.Lon + (r.Float64()*2-1)*c.SpanDeg
}

func Generate(cfg Config) (*Dataset, error) {
	if cfg.Vehicles < 1 || cfg.Tenants < 1 || len(cfg.Models) == 0 {
		return nil, fmt.Errorf("masterdata: need vehicles≥1, tenants≥1, models≥1")
	}
	for _, m := range cfg.Models {
		if _, ok := cfg.WMI[m.OEM]; !ok {
			return nil, fmt.Errorf("masterdata: no WMI for oem %q (model %s)", m.OEM, m.Code)
		}
	}
	r := rand.New(rand.NewPCG(cfg.Seed, 0x6b696c6f77617474)) // stream "kilowatt"
	ds := &Dataset{}

	// Tenants and one fleet per (tenant, city).
	for t := 0; t < cfg.Tenants; t++ {
		ten := Tenant{uuid(r), fmt.Sprintf("Synthetic Logistics %c", 'A'+t%26)}
		if t >= 26 {
			ten.Name += fmt.Sprint(t / 26)
		}
		ds.Tenants = append(ds.Tenants, ten)
		for _, c := range Cities {
			ds.Fleets = append(ds.Fleets, Fleet{uuid(r), ten.ID, ten.Name + " / " + c.Name, c.Name})
		}
	}

	// Assign each vehicle to a fleet, then size depots per fleet.
	fleetOf := make([]int, cfg.Vehicles)
	perFleet := make([]int, len(ds.Fleets))
	for i := range fleetOf {
		f := r.IntN(len(ds.Fleets))
		fleetOf[i], perFleet[f] = f, perFleet[f]+1
	}
	cityOf := map[string]City{}
	for _, c := range Cities {
		cityOf[c.Name] = c
	}
	depotsOf := make([][]int, len(ds.Fleets)) // fleet → depot indexes
	vehAtDepot := []int{}
	for fi, f := range ds.Fleets {
		n := (perFleet[fi] + VehiclesPerDepot - 1) / VehiclesPerDepot
		for d := 0; d < n; d++ {
			lat, lon := jitter(r, cityOf[f.City])
			depotsOf[fi] = append(depotsOf[fi], len(ds.Depots))
			ds.Depots = append(ds.Depots, Depot{uuid(r), f.TenantID, f.ID, fmt.Sprintf("%s Depot %d", f.City, len(ds.Depots)+1), f.City, lat, lon})
			vehAtDepot = append(vehAtDepot, 0)
		}
	}

	// Vehicles, packs, drivers, duties.
	serial := map[string]int{}
	placed := make([]int, len(ds.Fleets)) // round-robin cursor per fleet
	for i := 0; i < cfg.Vehicles; i++ {
		fi := fleetOf[i]
		f := ds.Fleets[fi]
		di := depotsOf[fi][placed[fi]%len(depotsOf[fi])]
		placed[fi]++
		vehAtDepot[di]++
		dep := ds.Depots[di]
		m := pickWeighted(r, cfg.Models, func(m Model) float64 { return m.Share })
		comm := commissionFrom.Add(time.Duration(r.Int64N(int64(commissionTo.Sub(commissionFrom)/(24*time.Hour))+1)) * 24 * time.Hour)
		wmi := cfg.WMI[m.OEM]
		serial[wmi]++
		v, err := vin.Build(wmi, m.Code, comm.Year(), cityOf[f.City].Plant, serial[wmi])
		if err != nil {
			return nil, err
		}
		pack := Pack{uuid(r), f.TenantID, fmt.Sprintf("PK-%s-%08d", m.Code, i+1), m.Code, comm}
		veh := Vehicle{uuid(r), f.TenantID, v, f.ID, m.Code, pack.ID, m.OEM, dep.ID, comm}
		ds.Packs = append(ds.Packs, pack)
		ds.Vehicles = append(ds.Vehicles, veh)
		ds.Drivers = append(ds.Drivers, Driver{uuid(r), f.TenantID, fmt.Sprintf("DRV-%016x", r.Uint64()), dep.ID})

		sh := pickWeighted(r, Shifts, func(s Shift) float64 { return s.Weight })
		km := m.DailyKmMin + r.IntN(m.DailyKmMax-m.DailyKmMin+1)
		ds.Duties = append(ds.Duties, Duty{veh.ID, f.TenantID, dep.ID, sh.Name, sh.DepartMin, sh.ReturnMin, km, RequiredSoC(km, m)})
	}

	// Depot charger sites (cap binds) and public sites.
	for di, d := range ds.Depots {
		site := ChargerSite{ID: uuid(r), TenantID: d.TenantID, DepotID: d.ID, Kind: "DEPOT", Name: d.Name, City: d.City, Lat: d.Lat, Lon: d.Lon, Geohash6: geo.Geohash(d.Lat, d.Lon, 6)}
		n := int(math.Ceil(float64(vehAtDepot[di]) * ChargersPerVeh))
		sum := 0.0
		for c := 0; c < n; c++ {
			ch := Charger{ID: uuid(r), TenantID: d.TenantID, SiteID: site.ID, OCPPID: fmt.Sprintf("CP-D%04d-%03d", di+1, c+1)}
			switch x := r.Float64(); {
			case x < 0.35:
				ch.ConnectorType, ch.MaxKW = "TYPE2_AC", 11
			case x < 0.70:
				ch.ConnectorType, ch.MaxKW = "TYPE2_AC", 22
			default:
				ch.ConnectorType, ch.MaxKW = "CCS2", 60
			}
			sum += ch.MaxKW
			ds.Chargers = append(ds.Chargers, ch)
		}
		site.PowerCapKW = math.Round(sum * SiteCapFactor)
		ds.Sites = append(ds.Sites, site)
	}
	for _, c := range Cities {
		for p := 0; p < PublicSitesPerCity; p++ {
			lat, lon := jitter(r, c)
			site := ChargerSite{ID: uuid(r), Kind: "PUBLIC", Name: fmt.Sprintf("%s Public %d", c.Name, p+1), City: c.Name, Lat: lat, Lon: lon, Geohash6: geo.Geohash(lat, lon, 6)}
			n, sum := 2+r.IntN(5), 0.0
			for k := 0; k < n; k++ {
				kw := 60.0
				if r.IntN(3) == 0 {
					kw = 120
				}
				sum += kw
				ds.Chargers = append(ds.Chargers, Charger{ID: uuid(r), SiteID: site.ID, OCPPID: fmt.Sprintf("CP-%c%03d-%02d", c.Plant, p+1, k+1), ConnectorType: "CCS2", MaxKW: kw})
			}
			site.PowerCapKW = math.Round(sum * 0.8)
			ds.Sites = append(ds.Sites, site)
		}
	}
	return ds, nil
}

// RequiredSoC is the departure SoC needed for plannedKm with a 15 pp reserve, clamped to [20, 100].
// Usable capacity is taken as 95% of nominal.
func RequiredSoC(plannedKm int, m Model) int {
	need := float64(plannedKm*m.WhPerKm) / 1000 / (m.CapacityKWh * 0.95) * 100
	return int(math.Min(100, math.Max(20, math.Ceil(need)+15)))
}
