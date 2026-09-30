package encoders

import (
	"bytes"
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/vin"
	"github.com/g-s-jithesh/Amazing-Coders/services/simulator/internal/domain/telemetry"
)

var update = flag.Bool("update", false, "rewrite libs/oem-samples golden files")

var samplesDir = filepath.Join("..", "..", "..", "..", "..", "libs", "oem-samples")

func TestGoldenVINs(t *testing.T) {
	if !vin.Valid(GoldenVIN) {
		d, _ := vin.CheckDigit(GoldenVIN)
		t.Fatalf("GoldenVIN %s invalid; check digit should be %c", GoldenVIN, d)
	}
	if bad := GoldenCases()["malformed_vin_checksum"].VIN; vin.Valid(bad) || len(bad) != 17 {
		t.Fatalf("malformed VIN %s must be 17 chars with a wrong check digit", bad)
	}
}

func TestEncodeAMetricISO(t *testing.T) {
	s := GoldenSample()
	b, err := EncodeA(&s, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["ts"] != "2026-09-01T05:00:00.123Z" || m["vin"] != GoldenVIN || m["evt"] != "DTC_RAISED" || m["soc_pct"].(float64) != 63.25 {
		t.Fatalf("oem_a fields: %s", b)
	}
	if pack := m["pack"].(map[string]any); pack["temp_max_c"].(float64) < 33 || pack["cell_min_mv"].(float64) != 3765 {
		t.Fatalf("oem_a pack: %v", pack)
	}
}

func TestEncodeBImperialBatch(t *testing.T) {
	s := GoldenSample()
	r1, _ := EncodeB(&s, nil)
	s.Seq++
	r2, _ := EncodeB(&s, nil)
	var recs []map[string]any
	if err := json.Unmarshal(JoinBatch([][]byte{r1, r2}, nil), &recs); err != nil || len(recs) != 2 {
		t.Fatalf("batch: %v", err)
	}
	r := recs[0]
	bat := r["battery"].(map[string]any)
	loc := r["location"].(map[string]any)
	if r["epoch"].(float64) != 1788238800 || // seconds, ms dropped
		math.Abs(bat["soc"].(float64)-0.6325) > 1e-6 ||
		math.Abs(bat["tempMaxF"].(float64)-(33.1*9/5+32)) > 0.01 ||
		math.Abs(r["ambientF"].(float64)-88.7) > 0.01 ||
		math.Abs(r["odometerMiles"].(float64)-18234.125/1.609344) > 1e-6 ||
		math.Abs(loc["speedMph"].(float64)-42.5/1.609344) > 1e-6 {
		t.Fatalf("oem_b units wrong: %s", r1)
	}
	if string(JoinBatch(nil, nil)) != "[]" {
		t.Fatal("empty batch")
	}
}

func TestEncodeCPositional(t *testing.T) {
	s := GoldenSample()
	b, _ := EncodeC(&s, nil)
	f := strings.Split(string(b), ";")
	if len(f) != len(strings.Split(CFields, ";")) {
		t.Fatalf("oem_c has %d fields, want %d: %s", len(f), len(strings.Split(CFields, ";")), b)
	}
	if f[1] != GoldenVIN || f[2] != "1788238800123" || f[4] != "8" || f[17] != "1" || f[22] != "0A7EC111" {
		t.Fatalf("oem_c fields: %s", b)
	}
}

func TestDropoutOmitsFields(t *testing.T) {
	c := GoldenCases()
	for _, enc := range []Encoder{EncodeA, EncodeB} {
		s := c["dropout_gps"]
		b, _ := enc(&s, nil)
		if bytes.Contains(b, []byte(`"lat`)) || bytes.Contains(b, []byte(`"latitude"`)) || bytes.Contains(b, []byte(`speed`)) {
			t.Errorf("GPS dropout still has position: %s", b)
		}
		s = c["dropout_temp"]
		if b, _ = enc(&s, nil); bytes.Contains(b, []byte("emp")) && !bytes.Contains(b, []byte("ambient")) {
			t.Errorf("temp dropout still has temps: %s", b)
		}
	}
	s := c["dropout_cells"]
	b, _ := EncodeC(&s, nil)
	if f := strings.Split(string(b), ";"); f[14] != "" || f[15] != "" || len(f) != 23 {
		t.Errorf("oem_c cell dropout: %s", b)
	}
	s = c["dropout_gps"]
	b, _ = EncodeC(&s, nil)
	if f := strings.Split(string(b), ";"); f[5] != "" || f[6] != "" || f[7] != "" || len(f) != 23 {
		t.Errorf("oem_c gps dropout: %s", b)
	}
	s = c["dropout_temp"]
	b, _ = EncodeC(&s, nil)
	if f := strings.Split(string(b), ";"); f[12] != "" || f[13] != "" || len(f) != 23 {
		t.Errorf("oem_c temp dropout: %s", b)
	}
}

func TestMalformedDecodeIsUnparseable(t *testing.T) {
	s := GoldenCases()["malformed_decode"]
	for oem, enc := range map[string]Encoder{"oem_a": EncodeA, "oem_b": EncodeB} {
		b, _ := enc(&s, []byte("prefix"))
		if !bytes.HasPrefix(b, []byte("prefix")) || json.Valid(b[6:]) {
			t.Errorf("%s decode-malformed payload still parses or lost prefix", oem)
		}
	}
	b, _ := EncodeC(&s, nil)
	if n := len(strings.Split(string(b), ";")); n >= 23 {
		t.Errorf("oem_c decode-malformed still has %d fields", n)
	}
	d := GoldenCases()["malformed_dtc_format"]
	if b, _ = EncodeC(&d, nil); !bytes.HasSuffix(b, []byte(";ZZZZ")) {
		t.Errorf("unencodable DTC should become ZZZZ: %s", b)
	}
}

// Golden files are the contract with the ingest-gateway adapters. Regenerate with:
//
//	go test ./internal/adapters/encoders -run TestGoldenFiles -update
func TestGoldenFiles(t *testing.T) {
	for oem := range ByOEM {
		files, err := Golden(oem)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(samplesDir, oem)
		for name, want := range files {
			path := filepath.Join(dir, name)
			if *update {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s/%s drifted from the encoder; regenerate with -update and rerun gateway tests", oem, name)
			}
		}
	}
	if _, err := Golden("oem_z"); err == nil {
		t.Error("unknown OEM must error")
	}
}

func TestNaNIsRejectedNotSilentlyEncoded(t *testing.T) {
	s := GoldenSample()
	s.PackVoltageV = float32(math.NaN())
	if _, err := EncodeA(&s, nil); err == nil {
		t.Error("oem_a must refuse NaN (JSON cannot carry it)")
	}
	if _, err := EncodeB(&s, nil); err == nil {
		t.Error("oem_b must refuse NaN")
	}
	_ = telemetry.WellFormed
}
