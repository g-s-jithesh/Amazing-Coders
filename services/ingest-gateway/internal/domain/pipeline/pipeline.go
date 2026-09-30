// Package pipeline is the record-level validation chain (Chain of Responsibility, CLAUDE.md §17):
// ordered, independent steps; the first rejection wins. Message-level steps (auth, size, raw
// publish, adapter selection, decode) run in app before this chain; dedup, enrich and publish after.
// Pure and allocation-light: O(len(DTC)) per record.
package pipeline

import (
	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/dtc"
	"github.com/g-s-jithesh/Amazing-Coders/libs/go-common/vin"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// Meta is what the transport knows about where a record came from.
type Meta struct {
	OEM        string // from topic / URL path; the adapter was chosen by it
	TopicVIN   string // MQTT topic VIN ("" for HTTPS batches)
	ReceivedMs int64
}

// Step inspects one decoded record.
type Step struct {
	Name  string
	Check func(m *Meta, e *canonical.Event) *canonical.Reject
}

// Chain runs steps in order and returns the first rejection.
type Chain []Step

func (c Chain) Run(m *Meta, e *canonical.Event) *canonical.Reject {
	for _, s := range c {
		if r := s.Check(m, e); r != nil {
			return r
		}
	}
	return nil
}

// isVINChars reports 17 chars from [A-HJ-NPR-Z0-9].
func isVINChars(v string) bool {
	if len(v) != 17 {
		return false
	}
	for i := 0; i < 17; i++ {
		c := v[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z') || c == 'I' || c == 'O' || c == 'Q' {
			return false
		}
	}
	return true
}

// Default is the production order (services/ingest-gateway/CLAUDE.md "Pipeline order", steps 6–9
// plus the identity check that needs the decoded VIN). wmiOEM maps WMI → the OEM allowed to send it.
func Default(wmiOEM map[string]string) Chain {
	return Chain{
		{"schema_version", func(_ *Meta, e *canonical.Event) *canonical.Reject {
			if e.SchemaVersion != canonical.SupportedSchema {
				return canonical.Rejectf(canonical.UnknownSchemaVersion, "schema %d", e.SchemaVersion)
			}
			return nil
		}},
		{"vin_format", func(_ *Meta, e *canonical.Event) *canonical.Reject {
			if !isVINChars(e.VIN) {
				return canonical.Rejectf(canonical.VINFormat, "vin %q", e.VIN)
			}
			return nil
		}},
		{"vin_checksum", func(_ *Meta, e *canonical.Event) *canonical.Reject {
			if !vin.Valid(e.VIN) {
				return canonical.Rejectf(canonical.VINChecksum, "vin %s", e.VIN)
			}
			return nil
		}},
		// Anti-spoofing (STRIDE "S"): an MQTT topic VIN must equal the payload VIN, and the VIN's
		// manufacturer (WMI) must belong to the OEM that sent it.
		{"identity", func(m *Meta, e *canonical.Event) *canonical.Reject {
			if m.TopicVIN != "" && m.TopicVIN != e.VIN {
				return canonical.Rejectf(canonical.IdentityMismatch, "topic vin %s ≠ payload vin %s", m.TopicVIN, e.VIN)
			}
			if owner, ok := wmiOEM[e.VIN[:3]]; !ok || owner != m.OEM {
				return canonical.Rejectf(canonical.IdentityMismatch, "vin %s (wmi %s) not registered to %s", e.VIN, e.VIN[:3], m.OEM)
			}
			return nil
		}},
		{"dtc_format", func(_ *Meta, e *canonical.Event) *canonical.Reject {
			for _, c := range e.DTC {
				if !dtc.Valid(c) {
					return canonical.Rejectf(canonical.DTCFormat, "dtc %q", c)
				}
			}
			return nil
		}},
		{"range", func(m *Meta, e *canonical.Event) *canonical.Reject { return e.CheckRange(m.ReceivedMs) }},
	}
}
