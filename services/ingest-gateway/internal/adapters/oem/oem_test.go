package oem

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/pipeline"
)

var samples = filepath.Join("..", "..", "..", "..", "..", "libs", "oem-samples")

// Values of the simulator's encoders.GoldenSample (the contract behind every golden file).
const (
	goldenVIN = "0KCDV45N9RC000001"
	goldenTs  = int64(1788238800123)
	recvMs    = goldenTs + 500
)

// outcome runs one golden file through adapter + domain chain, like the gateway does.
func outcome(t *testing.T, oemID, file string) ([]canonical.Record, *canonical.Reject) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(samples, oemID, file))
	if err != nil {
		t.Fatal(err)
	}
	recs, rej := Registry[oemID].Decode(body, oemID == "oem_b")
	if rej != nil {
		return nil, rej
	}
	chain := pipeline.Default(map[string]string{"0KC": oemID}) // the golden VIN's WMI, registered to the OEM under test
	for i := range recs {
		if recs[i].Reject == nil {
			recs[i].Reject = chain.Run(&pipeline.Meta{OEM: oemID, ReceivedMs: recvMs}, &recs[i].Event)
		}
	}
	return recs, nil
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestGoldenContract(t *testing.T) {
	want := map[string]canonical.Reason{
		"malformed_vin_checksum":   canonical.VINChecksum,
		"malformed_dtc_format":     canonical.DTCFormat,
		"malformed_schema_version": canonical.UnknownSchemaVersion,
		"malformed_range":          canonical.Range,
		"malformed_decode":         canonical.Decode,
	}
	dropout := map[string]uint8{"valid": canonical.HasAll, "dropout_gps": canonical.HasTemp | canonical.HasCells,
		"dropout_temp": canonical.HasGPS | canonical.HasCells, "dropout_cells": canonical.HasGPS | canonical.HasTemp}
	for oemID := range Registry {
		files, _ := os.ReadDir(filepath.Join(samples, oemID))
		if len(files) != 9 {
			t.Fatalf("%s: %d golden files, want 9", oemID, len(files))
		}
		for _, f := range files {
			name := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			recs, msgRej := outcome(t, oemID, f.Name())
			got := canonical.Reason("")
			if msgRej != nil {
				got = msgRej.Reason
			} else if len(recs) != 1 {
				t.Fatalf("%s/%s: %d records", oemID, name, len(recs))
			} else if recs[0].Reject != nil {
				got = recs[0].Reject.Reason
			}
			if exp, bad := want[name]; bad {
				if got != exp {
					t.Errorf("%s/%s: got %q, want %s", oemID, name, got, exp)
				}
				continue
			}
			if got != "" {
				t.Errorf("%s/%s: rejected %s: %v", oemID, name, got, recs[0].Reject)
				continue
			}
			e := recs[0].Event
			if e.Has != dropout[name] {
				t.Errorf("%s/%s: presence %03b, want %03b", oemID, name, e.Has, dropout[name])
			}
			if name != "valid" {
				continue
			}
			tsWant := goldenTs
			if oemID == "oem_b" {
				tsWant = goldenTs / 1000 * 1000 // epoch seconds: ms dropped by the OEM
			}
			if e.VIN != goldenVIN || e.OEM != oemID || e.TsEventMs != tsWant || e.Seq != 4242 || e.Evt != 8 || e.ChargeState != 1 ||
				!slices.Equal(e.DTC, []string{"P0A7E", "U0111"}) || !e.HVInterlockOK ||
				!near(float64(e.SoCPct), 63.25, 1e-4) || !near(float64(e.PackTempMaxC), 33.1, 1e-3) || !near(float64(e.AmbientC), 31.5, 1e-3) ||
				!near(e.OdoKm, 18234.125, 1e-6) || !near(float64(e.SpeedKmh), 42.5, 1e-3) || !near(e.Lat, 13.0827, 1e-9) ||
				e.CellVMinMv != 3765 || e.CellVMaxMv != 3779 || !near(float64(e.PackVoltageV), 362.4, 1e-3) {
				t.Errorf("%s/valid decoded to %+v", oemID, e)
			}
		}
	}
}

func TestSchemaErrors(t *testing.T) {
	cases := map[string]struct {
		oem  string
		body string
	}{
		"a missing vin":       {"oem_a", `{"schema":1,"ts":"2026-09-01T05:00:00Z","seq":1}`},
		"a soc wrong type":    {"oem_a", strings.Replace(read(t, "oem_a", "valid.json"), `"soc_pct":63.25`, `"soc_pct":"high"`, 1)},
		"a bad ts":            {"oem_a", strings.Replace(read(t, "oem_a", "valid.json"), `2026-09-01T05:00:00.123Z`, `yesterday`, 1)},
		"a unknown evt":       {"oem_a", strings.Replace(read(t, "oem_a", "valid.json"), `DTC_RAISED`, `EXPLODED`, 1)},
		"a partial pos":       {"oem_a", strings.Replace(read(t, "oem_a", "valid.json"), `"lat":13.0827,`, ``, 1)},
		"a schema not number": {"oem_a", `{"schema":"one"}`},
		"a not an object":     {"oem_a", `[1,2]`},
		"a no schema":         {"oem_a", `{"vin":"X"}`},
		"b no battery":        {"oem_b", `{"version":1}`},
		"c hvil":              {"oem_c", strings.Replace(read(t, "oem_c", "valid.txt"), ";1;13.90;", ";2;13.90;", 1)},
		"c soc not number":    {"oem_c", strings.Replace(read(t, "oem_c", "valid.txt"), ";63.25;", ";x;", 1)},
		"c partial gps":       {"oem_c", strings.Replace(read(t, "oem_c", "valid.txt"), ";13.082700;", ";;", 1)},
		"c schema not number": {"oem_c", "v" + read(t, "oem_c", "valid.txt")[1:]},
	}
	for name, c := range cases {
		recs, rej := Registry[c.oem].Decode([]byte(c.body), false)
		if rej != nil || len(recs) != 1 || recs[0].Reject == nil || recs[0].Reject.Reason != canonical.Schema {
			r := rej
			if rej == nil && len(recs) == 1 {
				r = recs[0].Reject
			}
			t.Errorf("%s: got %v, want SCHEMA", name, r)
		}
	}
}

func read(t *testing.T, oemID, file string) string {
	b, err := os.ReadFile(filepath.Join(samples, oemID, file))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// One bad record in a batch is rejected alone; its neighbours still decode.
func TestBatchIsolatesBadRecords(t *testing.T) {
	good := strings.Trim(read(t, "oem_b", "valid.json"), "[]")
	body := "[" + good + `,{"version":7},` + good + "]"
	recs, rej := B{}.Decode([]byte(body), true)
	if rej != nil || len(recs) != 3 || recs[0].Reject != nil || recs[2].Reject != nil || recs[1].Reject.Reason != canonical.UnknownSchemaVersion {
		t.Fatalf("recs=%+v rej=%v", recs, rej)
	}
	if string(recs[1].Raw) != `{"version":7}` {
		t.Fatalf("rejected record must carry only its own bytes: %s", recs[1].Raw)
	}
	line := read(t, "oem_c", "valid.txt")
	recs, _ = C{}.Decode([]byte(line+"\n1;broken\n"+line+"\r\n"), true)
	if len(recs) != 3 || recs[0].Reject != nil || recs[1].Reject.Reason != canonical.Decode || recs[2].Reject != nil {
		t.Fatalf("oem_c batch: %+v", recs)
	}
	if _, rej := A.Decode(A{}, []byte(`[{"schema":1}`), true); rej == nil || rej.Reason != canonical.Decode {
		t.Fatalf("unterminated batch must be DECODE, got %v", rej)
	}
}

func FuzzAdaptersNeverPanic(f *testing.F) {
	for oemID := range Registry {
		files, _ := os.ReadDir(filepath.Join(samples, oemID))
		for _, fi := range files {
			b, _ := os.ReadFile(filepath.Join(samples, oemID, fi.Name()))
			f.Add(b)
		}
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		for _, d := range Registry {
			d.Decode(body, false)
			d.Decode(body, true)
		}
	})
}
