package registry

import (
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

func TestRecordsMapMasterData(t *testing.T) {
	models := []masterdata.Model{{Code: "DV45N", OEM: "oem_c", CapacityKWh: 45, WhPerKm: 190, DailyKmMin: 90, DailyKmMax: 160, Share: 1}}
	ds, err := masterdata.Generate(masterdata.Config{Seed: 1, Vehicles: 50, Tenants: 1, Models: models, WMI: map[string]string{"oem_c": "0KC"}})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := Records(ds, models)
	if err != nil || len(recs) != 50 {
		t.Fatalf("%d records, %v", len(recs), err)
	}
	r, v, d := recs[7], ds.Vehicles[7], ds.Duties[7]
	if r.Vin != v.VIN || r.TenantId != v.TenantID || r.FleetId != v.FleetID || r.DepotId != v.HomeDepotID ||
		r.CapacityKwh != 45 || r.WhPerKm != 190 || int(r.DepartMinIst) != d.DepartMin || int(r.ReturnMinIst) != d.ReturnMin || r.DepotLat == 0 {
		t.Fatalf("record %+v", r)
	}
	ds.Duties[3].VehicleID = "mismatch"
	if _, err := Records(ds, models); err == nil {
		t.Fatal("inconsistent master data must error")
	}
}
