package fault

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCatalogueConsistency(t *testing.T) {
	for i, s := range Catalogue[1:] {
		if int(s.Kind) != i+1 {
			t.Fatalf("catalogue index %d holds kind %d", i+1, s.Kind)
		}
		if s.PrecursorMin < 24*time.Hour || s.PrecursorMax > 120*time.Hour || s.PrecursorMin > s.PrecursorMax {
			t.Errorf("%s precursor %v–%v outside 24–120 h", s.Name, s.PrecursorMin, s.PrecursorMax)
		}
		if len(s.DTC) == 0 || s.Weight <= 0 {
			t.Errorf("%s has no DTC or weight", s.Name)
		}
	}
}

// Every DTC we emit must be in the shared catalogue (battery-intel and stream-processor decode from it).
func TestDTCsExistInReferenceCatalogue(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "..", "..", "data", "reference", "dtc_catalogue.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]string{}
	for _, r := range rows[1:] {
		known[r[0]] = r[4] // code → sim_fault
	}
	for _, s := range Catalogue[1:] {
		for _, code := range s.DTC {
			if sim, ok := known[code]; !ok || sim != s.Name {
				t.Errorf("%s: DTC %s missing from catalogue or mapped to %q", s.Name, code, sim)
			}
		}
	}
}

func TestParse(t *testing.T) {
	for _, s := range Catalogue[1:] {
		if k, err := Parse(s.Name); err != nil || k != s.Kind || k.String() != s.Name {
			t.Errorf("Parse(%q) = %v, %v", s.Name, k, err)
		}
	}
	if _, err := Parse("bogus"); err == nil {
		t.Error("want error for unknown fault")
	}
	if None.String() != "none" || Kind(99).String() != "none" {
		t.Error("None/invalid names")
	}
	vin, k, err := ParseInject("ANY-VIN-STRING:cooling_degradation")
	if err != nil || vin != "ANY-VIN-STRING" || k != CoolingDegradation {
		t.Errorf("ParseInject = %q %v %v", vin, k, err)
	}
	for _, bad := range []string{"novin", ":cell_drift", "VIN:bogus"} {
		if _, _, err := ParseInject(bad); err == nil {
			t.Errorf("ParseInject(%q) want error", bad)
		}
	}
}

func TestProgress(t *testing.T) {
	for _, c := range []struct {
		now  int64
		want float64
	}{{-5, 0}, {0, 0}, {50, 0.5}, {100, 1}, {500, 1}} {
		if got := Progress(0, 100, c.now); got != c.want {
			t.Errorf("Progress(now=%d) = %v, want %v", c.now, got, c.want)
		}
	}
	if Progress(10, 10, 0) != 1 {
		t.Error("zero-length precursor should be complete")
	}
}

// Precursor signals move monotonically in the "worse" direction as p grows, and are healthy at p=0.
func TestEffectsMonotonicAndHealthyAtZero(t *testing.T) {
	for _, s := range Catalogue[1:] {
		if e := EffectsAt(s.Kind, 0); e.ExtraHeatRiseC != 0 || e.ExtraImbalanceMv != 0 || e.IsolationFactor != 1 || e.AuxSagV != 0 || e.CoolingEff != 1 {
			t.Errorf("%s not healthy at p=0: %+v", s.Name, e)
		}
		prev := EffectsAt(s.Kind, 0)
		for p := 0.1; p <= 1.0001; p += 0.1 {
			e := EffectsAt(s.Kind, p)
			if e.CoolingEff > prev.CoolingEff || e.ExtraHeatRiseC < prev.ExtraHeatRiseC || e.ExtraImbalanceMv < prev.ExtraImbalanceMv ||
				e.IsolationFactor > prev.IsolationFactor || e.InterlockFlapPerH < prev.InterlockFlapPerH || e.AuxSagV < prev.AuxSagV {
				t.Errorf("%s not monotonic at p=%.1f", s.Name, p)
			}
			prev = e
		}
	}
	if e := EffectsAt(InsulationWear, 1); e.IsolationFactor > 0.041 {
		t.Errorf("insulation factor at p=1 = %v", e.IsolationFactor)
	}
	if EffectsAt(None, 1) != Healthy {
		t.Error("None must be healthy")
	}
}
