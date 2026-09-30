package seed

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/adapters/refdata"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/masterdata"
)

// Loads the committed reference CSVs, generates a small fleet and checks every table file is well formed.
func TestWriteFromRealReferenceData(t *testing.T) {
	ref := filepath.Join("..", "..", "..", "..", "..", "data", "reference")
	models, err := refdata.Models(ref)
	if err != nil {
		t.Fatal(err)
	}
	wmis, err := refdata.WMIs(ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) < 3 || len(wmis) != 3 {
		t.Fatalf("models=%d wmis=%d", len(models), len(wmis))
	}
	ds, err := masterdata.Generate(masterdata.Config{Seed: 1, Vehicles: 300, Tenants: 2, Models: models, WMI: wmis})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Write(dir, ds, models); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"tenant": 2, "fleet": 6, "vehicle": 300, "battery_pack": 300, "driver": 300, "vehicle_duty": 300, "vehicle_model": len(models),
		"depot": len(ds.Depots), "charger_site": len(ds.Sites), "charger": len(ds.Chargers)}
	for table, n := range want {
		f, err := os.Open(filepath.Join(dir, table+".csv"))
		if err != nil {
			t.Fatal(err)
		}
		recs, err := csv.NewReader(f).ReadAll() // also enforces equal column count per row
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if len(recs)-1 != n {
			t.Errorf("%s: %d rows, want %d", table, len(recs)-1, n)
		}
	}
}

func TestRefdataMissingDir(t *testing.T) {
	if _, err := refdata.Models(t.TempDir()); err == nil {
		t.Fatal("want error for missing vehicle_models.csv")
	}
}
