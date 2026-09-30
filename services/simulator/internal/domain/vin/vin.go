// Package vin builds and validates 17-character VINs (ISO 3779 / FMVSS 565 check digit).
package vin

import "fmt"

var weights = [17]int{8, 7, 6, 5, 4, 3, 2, 10, 0, 9, 8, 7, 6, 5, 4, 3, 2}

// value transliterates one VIN character; ok=false for I, O, Q and anything outside [A-Z0-9].
func value(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'A' && c <= 'H':
		return int(c-'A') + 1, true
	case c >= 'J' && c <= 'N':
		return int(c-'J') + 1, true
	case c == 'P':
		return 7, true
	case c == 'R':
		return 9, true
	case c >= 'S' && c <= 'Z':
		return int(c-'S') + 2, true
	}
	return 0, false
}

// CheckDigit computes position 9 for a 17-char VIN (position 9 itself is ignored). O(1).
func CheckDigit(v string) (byte, error) {
	if len(v) != 17 {
		return 0, fmt.Errorf("vin: length %d, want 17", len(v))
	}
	sum := 0
	for i := 0; i < 17; i++ {
		if i == 8 {
			continue
		}
		n, ok := value(v[i])
		if !ok {
			return 0, fmt.Errorf("vin: invalid character %q at position %d", v[i], i+1)
		}
		sum += n * weights[i]
	}
	if r := sum % 11; r != 10 {
		return byte('0' + r), nil
	}
	return 'X', nil
}

// Valid reports whether v is 17 chars from [A-HJ-NPR-Z0-9] with a correct check digit. O(1).
func Valid(v string) bool {
	d, err := CheckDigit(v)
	return err == nil && v[8] == d
}

// yearCodes cycles every 30 years; 2010 and 1980 are 'A'.
const yearCodes = "ABCDEFGHJKLMNPRSTVWXY123456789"

// YearCode returns the position-10 model-year character.
func YearCode(year int) byte {
	return yearCodes[((year-1980)%30+30)%30]
}

// Build assembles WMI(3) + VDS(5) + check + year + plant + serial(6) and fills in the check digit.
func Build(wmi, vds string, year int, plant byte, serial int) (string, error) {
	if len(wmi) != 3 || len(vds) != 5 || serial < 0 || serial > 999999 {
		return "", fmt.Errorf("vin: bad parts wmi=%q vds=%q serial=%d", wmi, vds, serial)
	}
	b := []byte(fmt.Sprintf("%s%s0%c%c%06d", wmi, vds, YearCode(year), plant, serial))
	d, err := CheckDigit(string(b))
	if err != nil {
		return "", err
	}
	b[8] = d
	return string(b), nil
}
