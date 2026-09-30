// Package dtc validates SAE J2012 diagnostic trouble codes and converts them to and from the
// 2-byte wire form used by oem_c. Shared by the simulator (encode) and the gateway (decode). O(1).
package dtc

import (
	"fmt"
	"regexp"
)

var re = regexp.MustCompile(`^[PCBU][0-3][0-9A-F]{3}$`)

// Valid reports whether code matches ^[PCBU][0-3][0-9A-F]{3}$.
func Valid(code string) bool { return re.MatchString(code) }

const systems = "PCBU"

// Encode packs a code into 2 bytes: bits 15-14 system (P,C,B,U), 13-12 first digit (0-3), then
// three hex nibbles. P0A7E → 0x0A7E, U0111 → 0xC111.
func Encode(code string) (uint16, error) {
	if !Valid(code) {
		return 0, fmt.Errorf("dtc: invalid code %q", code)
	}
	var sys uint16
	for i := range systems {
		if systems[i] == code[0] {
			sys = uint16(i)
		}
	}
	v := sys<<14 | uint16(code[1]-'0')<<12
	for i, c := range code[2:] {
		n := uint16(c - '0')
		if c >= 'A' {
			n = uint16(c-'A') + 10
		}
		v |= n << (8 - 4*i)
	}
	return v, nil
}

// Decode is the inverse of Encode; every uint16 maps to a valid code.
func Decode(v uint16) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{systems[v>>14], byte('0' + (v>>12)&3), hex[(v>>8)&0xF], hex[(v>>4)&0xF], hex[v&0xF]})
}
