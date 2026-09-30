package dtc

import "testing"

func TestKnownCodes(t *testing.T) {
	for code, want := range map[string]uint16{"P0A7E": 0x0A7E, "U0111": 0xC111, "B1234": 0x9234, "C0300": 0x4300, "P3FFF": 0x3FFF, "P0000": 0} {
		got, err := Encode(code)
		if err != nil || got != want {
			t.Errorf("Encode(%s) = %04X, %v; want %04X", code, got, err, want)
		}
		if d := Decode(want); d != code {
			t.Errorf("Decode(%04X) = %s, want %s", want, d, code)
		}
	}
}

func TestInvalid(t *testing.T) {
	for _, bad := range []string{"PX12Z", "P4000", "X0A7E", "P0A7", "P0A7EE", "P0g00", "p0a7e", ""} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
		if _, err := Encode(bad); err == nil {
			t.Errorf("Encode(%q) want error", bad)
		}
	}
}

// Every 16-bit value decodes to a valid code that encodes back to itself.
func TestRoundTripAllValues(t *testing.T) {
	for v := 0; v <= 0xFFFF; v++ {
		code := Decode(uint16(v))
		if !Valid(code) {
			t.Fatalf("Decode(%04X) = %q invalid", v, code)
		}
		if back, err := Encode(code); err != nil || back != uint16(v) {
			t.Fatalf("round trip %04X → %s → %04X", v, code, back)
		}
	}
}

func FuzzValidNeverPanics(f *testing.F) {
	f.Add("P0A7E")
	f.Fuzz(func(t *testing.T, s string) {
		if Valid(s) {
			if v, err := Encode(s); err != nil || Decode(v) != s {
				t.Fatalf("valid %q failed round trip", s)
			}
		}
	})
}
