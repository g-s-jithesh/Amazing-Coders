package oem

import (
	"bytes"
	"strconv"

	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/dtc"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// C decodes oem_c: one ';'-separated line per record (23 positional fields, layout v1 below),
// DTCs as concatenated 4-hex-digit SAE J2012 words. HTTPS batches are newline-separated lines.
//
//	schema;vin;ts_ms;seq;evt;lat;lon;speed_kmh;odo_km;soc_pct;pack_v;pack_a;t_min_c;t_max_c;
//	cell_min_mv;cell_max_mv;iso_kohm;hvil;aux_v;amb_c;charge_state;charge_kw;dtc_hex
type C struct{}

const cFields = 23

func (C) Decode(body []byte, batch bool) ([]canonical.Record, *canonical.Reject) {
	lines := [][]byte{body}
	if batch {
		lines = bytes.Split(bytes.TrimRight(body, "\n"), []byte{'\n'})
	}
	out := make([]canonical.Record, len(lines))
	for i, l := range lines {
		out[i] = decodeC(bytes.TrimRight(l, "\r"))
	}
	return out, nil
}

// cParser collects the first field error so the mapping code stays linear.
type cParser struct {
	f   [][]byte
	bad string
}

func (p *cParser) float(i int, bits int) float64 {
	v, err := strconv.ParseFloat(string(p.f[i]), bits)
	if err != nil && p.bad == "" {
		p.bad = strconv.Itoa(i)
	}
	return v
}

func (p *cParser) int(i int) int64 {
	v, err := strconv.ParseInt(string(p.f[i]), 10, 64)
	if err != nil && p.bad == "" {
		p.bad = strconv.Itoa(i)
	}
	return v
}

func (p *cParser) uint(i int, bits int) uint64 {
	v, err := strconv.ParseUint(string(p.f[i]), 10, bits)
	if err != nil && p.bad == "" {
		p.bad = strconv.Itoa(i)
	}
	return v
}

// group reports whether fields [lo, hi] are all present; partial groups are a schema error.
func (p *cParser) group(lo, hi int) (present bool, partial bool) {
	n := 0
	for i := lo; i <= hi; i++ {
		if len(p.f[i]) > 0 {
			n++
		}
	}
	return n == hi-lo+1, n != 0 && n != hi-lo+1
}

func decodeC(raw []byte) canonical.Record {
	f := bytes.Split(raw, []byte{';'})
	if len(f) != cFields {
		return reject(raw, canonical.Decode, "%d fields, want %d", len(f), cFields)
	}
	ver, err := strconv.ParseUint(string(f[0]), 10, 32)
	if err != nil {
		return reject(raw, canonical.Schema, "schema field %q", f[0])
	}
	if ver != canonical.SupportedSchema {
		return reject(raw, canonical.UnknownSchemaVersion, "schema %d", ver)
	}
	p := &cParser{f: f}
	e := canonical.Event{
		SchemaVersion: uint32(ver), VIN: string(f[1]), OEM: "oem_c",
		TsEventMs: p.int(2), Seq: p.uint(3, 64), Evt: int32(p.int(4)),
		OdoKm: p.float(8, 64), SoCPct: float32(p.float(9, 32)),
		PackVoltageV: float32(p.float(10, 32)), PackCurrentA: float32(p.float(11, 32)),
		IsolationKohm: float32(p.float(16, 32)), Aux12vV: float32(p.float(18, 32)), AmbientC: float32(p.float(19, 32)),
		ChargeState: int32(p.int(20)), ChargePowerKW: float32(p.float(21, 32)),
	}
	switch string(f[17]) {
	case "1":
		e.HVInterlockOK = true
	case "0":
	default:
		return reject(raw, canonical.Schema, "hvil %q", f[17])
	}
	for _, g := range []struct {
		lo, hi int
		bit    uint8
		name   string
	}{{5, 7, canonical.HasGPS, "gps"}, {12, 13, canonical.HasTemp, "temperature"}, {14, 15, canonical.HasCells, "cell"}} {
		present, partial := p.group(g.lo, g.hi)
		if partial {
			return reject(raw, canonical.Schema, "partial %s group", g.name)
		}
		if present {
			e.Has |= g.bit
		}
	}
	if e.Has&canonical.HasGPS != 0 {
		e.Lat, e.Lon, e.SpeedKmh = p.float(5, 64), p.float(6, 64), float32(p.float(7, 32))
	}
	if e.Has&canonical.HasTemp != 0 {
		e.PackTempMinC, e.PackTempMaxC = float32(p.float(12, 32)), float32(p.float(13, 32))
	}
	if e.Has&canonical.HasCells != 0 {
		e.CellVMinMv, e.CellVMaxMv = uint32(p.uint(14, 32)), uint32(p.uint(15, 32))
	}
	if p.bad != "" {
		return reject(raw, canonical.Schema, "field %s is not a number", p.bad)
	}
	hexs := f[22]
	if len(hexs)%4 != 0 {
		return reject(raw, canonical.DTCFormat, "dtc hex length %d", len(hexs))
	}
	for i := 0; i < len(hexs); i += 4 {
		v, err := strconv.ParseUint(string(hexs[i:i+4]), 16, 16)
		if err != nil {
			return reject(raw, canonical.DTCFormat, "dtc word %q", hexs[i:i+4])
		}
		e.DTC = append(e.DTC, dtc.Decode(uint16(v)))
	}
	return canonical.Record{Raw: raw, Event: e}
}
