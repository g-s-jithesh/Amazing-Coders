// Package groundtruth writes the simulator's truth (true SoH per pack per day, injected faults) to
// Parquet. It is the leakage guard of CLAUDE.md §6: only ml/ evaluation code may read these files,
// never the production pipeline. The simulator is the only writer.
package groundtruth

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/parquet-go/parquet-go"

	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/vehicle"
)

// FaultRow / SoHRow are the on-disk schemas (snake_case columns, UTC epoch ms).
type FaultRow struct {
	VIN              string `parquet:"vin"`
	Fault            string `parquet:"fault"`
	DTC              string `parquet:"dtc"`
	PrecursorStartMs int64  `parquet:"precursor_start_ts_ms"`
	DTCMs            int64  `parquet:"dtc_ts_ms"`
	RepairMs         int64  `parquet:"repair_ts_ms"`
	Injected         bool   `parquet:"injected"`
}

type SoHRow struct {
	VIN     string  `parquet:"vin"`
	AsOfMs  int64   `parquet:"as_of_ts_ms"`
	SoHPct  float64 `parquet:"soh_true_pct"`
	QCalPct float64 `parquet:"fade_calendar_pct"`
	QCycPct float64 `parquet:"fade_cycle_pct"`
	EFC     float64 `parquet:"efc"`
}

// Writer appends truth rows to <dir>/faults-<run>.parquet and <dir>/soh_daily-<run>.parquet.
type Writer struct {
	faultF, sohF *os.File
	faults       *parquet.GenericWriter[FaultRow]
	soh          *parquet.GenericWriter[SoHRow]
}

func Open(dir, runID string) (*Writer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ff, err := os.Create(filepath.Join(dir, fmt.Sprintf("faults-%s.parquet", runID)))
	if err != nil {
		return nil, err
	}
	sf, err := os.Create(filepath.Join(dir, fmt.Sprintf("soh_daily-%s.parquet", runID)))
	if err != nil {
		ff.Close()
		return nil, err
	}
	zstd := parquet.Compression(&parquet.Zstd)
	return &Writer{ff, sf, parquet.NewGenericWriter[FaultRow](ff, zstd), parquet.NewGenericWriter[SoHRow](sf, zstd)}, nil
}

func (w *Writer) Faults(recs []vehicle.FaultTruth) error {
	if len(recs) == 0 {
		return nil
	}
	rows := make([]FaultRow, len(recs))
	for i, r := range recs {
		rows[i] = FaultRow{r.VIN, r.Fault, r.DTC, r.PrecursorStartMs, r.DTCMs, r.RepairMs, r.Injected}
	}
	_, err := w.faults.Write(rows)
	return err
}

func (w *Writer) SoH(recs []app.SoHTruth) error {
	if len(recs) == 0 {
		return nil
	}
	rows := make([]SoHRow, len(recs))
	for i, r := range recs {
		rows[i] = SoHRow{r.VIN, r.AsOfMs, r.SoHPct, r.QCalPct, r.QCycPct, r.EFC}
	}
	_, err := w.soh.Write(rows)
	return err
}

// Close flushes both files; the Parquet footer is only valid after Close.
func (w *Writer) Close() error {
	var first error
	for _, c := range []func() error{w.faults.Close, w.soh.Close, w.faultF.Close, w.sohF.Close} {
		if err := c(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
