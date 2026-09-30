package masterdata

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/vin"
)

var testModels = []Model{
	{Code: "CV21L", OEM: "oem_a", CapacityKWh: 21, WhPerKm: 140, DailyKmMin: 80, DailyKmMax: 140, Share: 0.6},
	{Code: "DV45N", OEM: "oem_c", CapacityKWh: 45, WhPerKm: 190, DailyKmMin: 90, DailyKmMax: 160, Share: 0.4},
}

func cfg(seed uint64, n int) Config {
	return Config{Seed: seed, Vehicles: n, Tenants: 3, Models: testModels, WMI: map[string]string{"oem_a": "0KA", "oem_c": "0KC"}}
}

func digest(t *testing.T, c Config) [32]byte {
	t.Helper()
	ds, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%+v", *ds)))
}

func TestDeterministic(t *testing.T) {
	if digest(t, cfg(42, 2000)) != digest(t, cfg(42, 2000)) {
		t.Fatal("same seed produced different data")
	}
	if digest(t, cfg(42, 2000)) == digest(t, cfg(43, 2000)) {
		t.Fatal("different seeds produced identical data")
	}
}

func TestInvariants(t *testing.T) {
	const n = 5000
	ds, err := Generate(cfg(7, n))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Vehicles) != n || len(ds.Packs) != n || len(ds.Duties) != n || len(ds.Drivers) != n {
		t.Fatalf("row counts: veh=%d packs=%d duties=%d drivers=%d", len(ds.Vehicles), len(ds.Packs), len(ds.Duties), len(ds.Drivers))
	}
	if len(ds.Fleets) != 3*len(Cities) {
		t.Fatalf("fleets = %d", len(ds.Fleets))
	}

	ids := map[string]bool{}
	unique := func(kind, id string) {
		if ids[id] {
			t.Fatalf("duplicate id %s in %s", id, kind)
		}
		ids[id] = true
	}
	tenants, fleets, depots, sites := map[string]bool{}, map[string]string{}, map[string]string{}, map[string]ChargerSite{}
	for _, x := range ds.Tenants {
		unique("tenant", x.ID)
		tenants[x.ID] = true
	}
	for _, x := range ds.Fleets {
		unique("fleet", x.ID)
		fleets[x.ID] = x.TenantID
	}
	perDepot := map[string]int{}
	for _, x := range ds.Depots {
		unique("depot", x.ID)
		if fleets[x.FleetID] != x.TenantID {
			t.Fatalf("depot %s fleet/tenant mismatch", x.ID)
		}
		depots[x.ID] = x.TenantID
	}
	vins := map[string]bool{}
	packTenant := map[string]string{}
	for _, p := range ds.Packs {
		unique("pack", p.ID)
		packTenant[p.ID] = p.TenantID
	}
	for _, v := range ds.Vehicles {
		unique("vehicle", v.ID)
		if !vin.Valid(v.VIN) || vins[v.VIN] {
			t.Fatalf("bad or duplicate VIN %q", v.VIN)
		}
		vins[v.VIN] = true
		if v.VIN[:3] != map[string]string{"oem_a": "0KA", "oem_c": "0KC"}[v.OEM] {
			t.Fatalf("VIN %s WMI does not match oem %s", v.VIN, v.OEM)
		}
		if fleets[v.FleetID] != v.TenantID || depots[v.HomeDepotID] != v.TenantID || packTenant[v.PackID] != v.TenantID {
			t.Fatalf("vehicle %s crosses tenants", v.VIN)
		}
		perDepot[v.HomeDepotID]++
	}
	for id, c := range perDepot {
		if c > VehiclesPerDepot {
			t.Fatalf("depot %s has %d vehicles > %d", id, c, VehiclesPerDepot)
		}
	}
	for _, s := range ds.Sites {
		unique("site", s.ID)
		sites[s.ID] = s
		if (s.Kind == "DEPOT") != (s.TenantID != "" && s.DepotID != "") {
			t.Fatalf("site %s kind/tenant mismatch", s.ID)
		}
	}
	sumKW := map[string]float64{}
	for _, c := range ds.Chargers {
		unique("charger", c.ID)
		s, ok := sites[c.SiteID]
		if !ok || s.TenantID != c.TenantID {
			t.Fatalf("charger %s has bad site/tenant", c.ID)
		}
		sumKW[c.SiteID] += c.MaxKW
	}
	for _, s := range ds.Sites {
		if s.PowerCapKW <= 0 || s.PowerCapKW >= sumKW[s.ID] {
			t.Fatalf("site %s cap %.0f should be in (0, %.0f)", s.Name, s.PowerCapKW, sumKW[s.ID])
		}
		if len(s.Geohash6) != 6 {
			t.Fatalf("site %s geohash %q", s.Name, s.Geohash6)
		}
	}
	for _, d := range ds.Duties {
		if d.RequiredSoCPct < 20 || d.RequiredSoCPct > 100 || d.DepartMin < 0 || d.DepartMin >= 1440 || d.ReturnMin < 0 || d.ReturnMin >= 1440 {
			t.Fatalf("bad duty %+v", d)
		}
	}
}

func TestRequiredSoC(t *testing.T) {
	m := Model{CapacityKWh: 40, WhPerKm: 200}
	for km, want := range map[int]int{0: 20, 10: 21, 100: 68, 1000: 100} { // 100 km → 20 kWh / 38 kWh = 52.6% → 53 + 15
		if got := RequiredSoC(km, m); got != want {
			t.Errorf("RequiredSoC(%d) = %d, want %d", km, got, want)
		}
	}
}

func TestGenerateRejectsBadConfig(t *testing.T) {
	bad := []Config{
		{Vehicles: 0, Tenants: 1, Models: testModels},
		{Vehicles: 1, Tenants: 0, Models: testModels},
		{Vehicles: 1, Tenants: 1},
		{Vehicles: 1, Tenants: 1, Models: testModels, WMI: map[string]string{"oem_a": "0KA"}}, // oem_c missing
	}
	for i, c := range bad {
		if _, err := Generate(c); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
}
