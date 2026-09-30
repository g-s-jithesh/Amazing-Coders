// Package alert is the per-(vehicle, rule, level) alert state machine with sustain and hysteresis,
// driven purely by event time:
//
//	OK ──breach──▶ PENDING ──breach held ≥ For──▶ FIRING ──clear held ≥ ClearFor──▶ OK (RESOLVED emitted)
//	      PENDING ──clear──▶ OK                     FIRING ──breach again──▶ FIRING (stays; no re-fire)
//
// An episode fires once. Its ID is UUIDv5(ns, vin|rule|episode_start_ms), so re-processing the same
// events (Kafka redelivery) yields the same ID and fleet-api's ON CONFLICT DO NOTHING absorbs it. O(1).
package alert

import (
	"crypto/sha1"
	"fmt"
	"strconv"
)

type Phase uint8

const (
	OK Phase = iota
	Pending
	Firing
)

type Transition uint8

const (
	None Transition = iota
	Fired
	Resolved
)

// Machine is the state for one (vin, rule, level).
type Machine struct {
	Phase     Phase
	StartMs   int64 // first breach of the current episode (the alert's window_start)
	ClearMs   int64 // first clear sample while firing (0 = not clearing)
	LastMs    int64
	ForMs     int64 // breach must hold this long before firing (0 = fire immediately)
	ClearForM int64 // clear must hold this long before resolving
}

// Step feeds one observation at event time tsMs. Observations older than the last one are ignored
// (rules run on per-VIN monotonic event time; late events are stored but never re-open alerts).
func (m *Machine) Step(tsMs int64, breach bool) Transition {
	if tsMs < m.LastMs {
		return None
	}
	m.LastMs = tsMs
	switch m.Phase {
	case OK:
		if breach {
			m.Phase, m.StartMs = Pending, tsMs
			if m.ForMs == 0 {
				m.Phase = Firing
				return Fired
			}
		}
	case Pending:
		switch {
		case !breach:
			m.Phase = OK
		case tsMs-m.StartMs >= m.ForMs:
			m.Phase = Firing
			return Fired
		}
	case Firing:
		switch {
		case breach:
			m.ClearMs = 0
		case m.ClearMs == 0:
			m.ClearMs = tsMs
			if m.ClearForM == 0 {
				m.Phase, m.ClearMs = OK, 0
				return Resolved
			}
		case tsMs-m.ClearMs >= m.ClearForM:
			m.Phase, m.ClearMs = OK, 0
			return Resolved
		}
	}
	return None
}

// namespace is a fixed UUID namespace for Kilowatt alerts (random v4, generated once).
var namespace = [16]byte{0x6f, 0x1c, 0x2d, 0x8a, 0x4b, 0x3e, 0x4f, 0x51, 0x9a, 0x77, 0x0c, 0x5e, 0x21, 0xd3, 0x88, 0x4b}

// ID is the deterministic alert id: RFC 9562 UUIDv5 over "vin|rule|startMs".
func ID(vin, rule string, startMs int64) string {
	h := sha1.New()
	h.Write(namespace[:])
	h.Write([]byte(vin + "|" + rule + "|" + strconv.FormatInt(startMs, 10)))
	s := h.Sum(nil)
	s[6] = s[6]&0x0F | 0x50
	s[8] = s[8]&0x3F | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", s[0:4], s[4:6], s[6:8], s[8:10], s[10:16])
}
