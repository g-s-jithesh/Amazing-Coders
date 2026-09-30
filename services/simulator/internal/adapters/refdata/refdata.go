// Package refdata loads data/reference CSVs into domain types.
package refdata

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

// readCSV returns rows as header→value maps.
func readCSV(path string) ([]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(recs) < 2 {
		return nil, fmt.Errorf("%s: no data rows", path)
	}
	out := make([]map[string]string, 0, len(recs)-1)
	for _, r := range recs[1:] {
		row := map[string]string{}
		for i, h := range recs[0] {
			row[h] = r[i]
		}
		out = append(out, row)
	}
	return out, nil
}

// WMIs maps oem → WMI from wmi_synthetic.csv.
func WMIs(dir string) (map[string]string, error) {
	rows, err := readCSV(filepath.Join(dir, "wmi_synthetic.csv"))
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r["oem"]] = r["wmi"]
	}
	return m, nil
}

// Models parses vehicle_models.csv.
func Models(dir string) ([]masterdata.Model, error) {
	path := filepath.Join(dir, "vehicle_models.csv")
	rows, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	var out []masterdata.Model
	for i, r := range rows {
		var perr error
		f := func(k string) float64 {
			v, err := strconv.ParseFloat(r[k], 64)
			if err != nil && perr == nil {
				perr = fmt.Errorf("%s row %d column %s: %w", path, i+2, k, err)
			}
			return v
		}
		out = append(out, masterdata.Model{
			Code: r["code"], Name: r["name"], OEM: r["oem"], Chemistry: r["chemistry"],
			CapacityKWh: f("nominal_capacity_kwh"), VoltageV: f("nominal_voltage_v"),
			MaxACKW: f("max_ac_kw"), MaxDCKW: f("max_dc_kw"),
			WhPerKm: int(f("consumption_wh_per_km")), DailyKmMin: int(f("daily_km_min")), DailyKmMax: int(f("daily_km_max")),
			Share: f("fleet_share"),
		})
		if perr != nil {
			return nil, perr
		}
	}
	return out, nil
}
