package groundtruth

import (
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	faults := []vehicle.FaultTruth{
		{VIN: "V1", Fault: "cell_drift", DTC: "P0A7F", PrecursorStartMs: 1, DTCMs: 2, RepairMs: 3},
		{VIN: "V2", Fault: "weak_aux_battery", DTC: "P0562,U0111", PrecursorStartMs: 4, DTCMs: 5, RepairMs: 6, Injected: true},
	}
	soh := []app.SoHTruth{{VIN: "V1", AsOfMs: 10, SoHPct: 93.5, QCalPct: 4, QCycPct: 2.5, EFC: 812}}
	if err := w.Faults(faults); err != nil {
		t.Fatal(err)
	}
	if err := w.Faults(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.SoH(soh); err != nil {
		t.Fatal(err)
	}
	if err := w.SoH(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	fr, err := parquet.ReadFile[FaultRow](filepath.Join(dir, "faults-test.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fr) != 2 || fr[1] != (FaultRow{"V2", "weak_aux_battery", "P0562,U0111", 4, 5, 6, true}) {
		t.Fatalf("faults = %+v", fr)
	}
	sr, err := parquet.ReadFile[SoHRow](filepath.Join(dir, "soh_daily-test.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sr) != 1 || sr[0] != (SoHRow{"V1", 10, 93.5, 4, 2.5, 812}) {
		t.Fatalf("soh = %+v", sr)
	}
}

func TestOpenFailsOnBadDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if w, err := Open(f, "x"); err != nil {
		t.Fatal(err) // dir is created
	} else {
		w.Close()
	}
	if _, err := Open(filepath.Join(f, "faults-x.parquet"), "y"); err == nil {
		t.Fatal("want error when dir path is an existing file")
	}
}
