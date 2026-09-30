package vin

import (
	"strings"
	"testing"
)

func TestValidKnownVINs(t *testing.T) {
	for _, v := range []string{"1M8GDM9AXKP042788", "11111111111111111"} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false, want true", v)
		}
	}
}

func TestValidRejects(t *testing.T) {
	for name, v := range map[string]string{
		"bad check digit": "1M8GDM9A1KP042788",
		"contains I":      "1M8GDM9AXKI042788",
		"contains O":      "1M8GDM9AXKO042788",
		"contains Q":      "1M8GDM9AXKQ042788",
		"lowercase":       "1m8gdm9axkp042788",
		"too short":       "1M8GDM9AXKP04278",
		"too long":        "1M8GDM9AXKP0427888",
		"empty":           "",
	} {
		if Valid(v) {
			t.Errorf("%s: Valid(%q) = true, want false", name, v)
		}
	}
}

func TestYearCode(t *testing.T) {
	for y, want := range map[int]byte{1980: 'A', 2010: 'A', 2021: 'M', 2024: 'R', 2026: 'T', 2031: '1', 2039: '9', 2040: 'A'} {
		if got := YearCode(y); got != want {
			t.Errorf("YearCode(%d) = %c, want %c", y, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	v, err := Build("0KA", "CV21L", 2024, 'B', 42)
	if err != nil {
		t.Fatal(err)
	}
	if !Valid(v) || !strings.HasPrefix(v, "0KACV21L") || !strings.HasSuffix(v, "RB000042") {
		t.Fatalf("Build = %q", v)
	}
	for _, bad := range []struct {
		wmi, vds string
		serial   int
	}{{"0K", "CV21L", 1}, {"0KA", "CV21", 1}, {"0KA", "CV21L", -1}, {"0KA", "CV21L", 1_000_000}, {"0KI", "CV21L", 1}} {
		if _, err := Build(bad.wmi, bad.vds, 2024, 'B', bad.serial); err == nil {
			t.Errorf("Build(%q,%q,%d) want error", bad.wmi, bad.vds, bad.serial)
		}
	}
}

// Every built VIN is valid, and replacing its check digit with any other character makes it invalid.
func FuzzBuildThenCorrupt(f *testing.F) {
	f.Add(uint32(42), uint16(2024), byte(3))
	f.Fuzz(func(t *testing.T, serial uint32, year uint16, pick byte) {
		v, err := Build("0KB", "SD30L", int(year), 'C', int(serial%1_000_000))
		if err != nil {
			t.Fatal(err)
		}
		if !Valid(v) {
			t.Fatalf("built VIN %q invalid", v)
		}
		alt := "0123456789X"[int(pick)%11]
		if alt == v[8] {
			return
		}
		if Valid(v[:8] + string(alt) + v[9:]) {
			t.Fatalf("corrupted check digit accepted for %q", v)
		}
	})
}

func FuzzValidNeverPanics(f *testing.F) {
	f.Add("1M8GDM9AXKP042788")
	f.Fuzz(func(t *testing.T, s string) { _ = Valid(s) })
}
