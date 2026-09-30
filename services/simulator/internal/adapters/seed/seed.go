// Package seed writes master data as one CSV per fleet-schema table (loaded with COPY).
// Column order must match services/fleet-api/migrations/0001_fleet_core.sql.
package seed

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

const dateFmt = "2006-01-02"

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
func geo(f float64) string { return strconv.FormatFloat(f, 'f', 6, 64) }

func write(dir, table string, header []string, n int, row func(i int) []string) error {
	f, err := os.Create(filepath.Join(dir, table+".csv"))
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	w := csv.NewWriter(bw)
	_ = w.Write(header)
	for i := 0; i < n; i++ {
		_ = w.Write(row(i))
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", table, err)
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Write emits every table into dir. Empty strings become NULL under COPY ... CSV.
func Write(dir string, ds *masterdata.Dataset, models []masterdata.Model) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	steps := []func() error{
		func() error {
			return write(dir, "tenant", []string{"id", "name"}, len(ds.Tenants), func(i int) []string {
				t := ds.Tenants[i]
				return []string{t.ID, t.Name}
			})
		},
		func() error {
			return write(dir, "fleet", []string{"id", "tenant_id", "name", "city"}, len(ds.Fleets), func(i int) []string {
				f := ds.Fleets[i]
				return []string{f.ID, f.TenantID, f.Name, f.City}
			})
		},
		func() error {
			return write(dir, "depot", []string{"id", "tenant_id", "fleet_id", "name", "city", "lat", "lon"}, len(ds.Depots), func(i int) []string {
				d := ds.Depots[i]
				return []string{d.ID, d.TenantID, d.FleetID, d.Name, d.City, geo(d.Lat), geo(d.Lon)}
			})
		},
		func() error {
			return write(dir, "vehicle_model", []string{"code", "name", "oem", "chemistry", "nominal_capacity_kwh", "nominal_voltage_v", "max_ac_kw", "max_dc_kw", "consumption_wh_per_km"}, len(models), func(i int) []string {
				m := models[i]
				return []string{m.Code, m.Name, m.OEM, m.Chemistry, num(m.CapacityKWh), num(m.VoltageV), num(m.MaxACKW), num(m.MaxDCKW), strconv.Itoa(m.WhPerKm)}
			})
		},
		func() error {
			return write(dir, "battery_pack", []string{"id", "tenant_id", "pack_serial", "model_code", "install_date"}, len(ds.Packs), func(i int) []string {
				p := ds.Packs[i]
				return []string{p.ID, p.TenantID, p.Serial, p.ModelCode, p.InstallDate.Format(dateFmt)}
			})
		},
		func() error {
			return write(dir, "vehicle", []string{"id", "tenant_id", "vin", "fleet_id", "model_code", "current_pack_id", "oem", "home_depot_id", "commissioned_on"}, len(ds.Vehicles), func(i int) []string {
				v := ds.Vehicles[i]
				return []string{v.ID, v.TenantID, v.VIN, v.FleetID, v.ModelCode, v.PackID, v.OEM, v.HomeDepotID, v.CommissionedOn.Format(dateFmt)}
			})
		},
		func() error {
			return write(dir, "driver", []string{"id", "tenant_id", "driver_ref", "depot_id"}, len(ds.Drivers), func(i int) []string {
				d := ds.Drivers[i]
				return []string{d.ID, d.TenantID, d.Ref, d.DepotID}
			})
		},
		func() error {
			return write(dir, "charger_site", []string{"id", "tenant_id", "depot_id", "kind", "name", "city", "lat", "lon", "geohash6", "site_power_cap_kw"}, len(ds.Sites), func(i int) []string {
				s := ds.Sites[i]
				return []string{s.ID, s.TenantID, s.DepotID, s.Kind, s.Name, s.City, geo(s.Lat), geo(s.Lon), s.Geohash6, num(s.PowerCapKW)}
			})
		},
		func() error {
			return write(dir, "charger", []string{"id", "tenant_id", "site_id", "ocpp_id", "connector_type", "max_kw"}, len(ds.Chargers), func(i int) []string {
				c := ds.Chargers[i]
				return []string{c.ID, c.TenantID, c.SiteID, c.OCPPID, c.ConnectorType, num(c.MaxKW)}
			})
		},
		func() error {
			return write(dir, "vehicle_duty", []string{"vehicle_id", "tenant_id", "depot_id", "shift", "depart_min_ist", "return_min_ist", "planned_km", "required_soc_pct"}, len(ds.Duties), func(i int) []string {
				d := ds.Duties[i]
				return []string{d.VehicleID, d.TenantID, d.DepotID, d.Shift, strconv.Itoa(d.DepartMin), strconv.Itoa(d.ReturnMin), strconv.Itoa(d.PlannedKm), strconv.Itoa(d.RequiredSoCPct)}
			})
		},
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}
