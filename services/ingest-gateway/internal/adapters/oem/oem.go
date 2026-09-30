// Package oem holds the OEM payload adapters (Adapter pattern) and their registry (Factory):
// wire bytes → canonical records. Each adapter is the inverse of the simulator encoder for that
// OEM; libs/oem-samples is the shared contract. Adding an OEM = one Decoder + registry entry +
// golden samples + contract test (services/ingest-gateway/CLAUDE.md "Adding an OEM").
//
// Reason precedence while decoding: unparseable → DECODE; schema version missing/wrong type →
// SCHEMA; version ≠ 1 → UNKNOWN_SCHEMA_VERSION; a required field missing or mistyped → SCHEMA.
// Semantic checks (VIN, identity, DTC, range) run afterwards in the domain pipeline.
package oem

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// Decoder turns one MQTT message (batch=false) or one HTTPS batch body (batch=true) into records.
// A non-nil *Reject means the whole body is unusable (e.g. the batch array itself is malformed).
type Decoder interface {
	Decode(body []byte, batch bool) ([]canonical.Record, *canonical.Reject)
}

// Registry maps an OEM id (from the MQTT topic or the HTTPS path) to its adapter.
var Registry = map[string]Decoder{"oem_a": A{}, "oem_b": B{}, "oem_c": C{}}

var evtByName = map[string]int32{"PERIODIC": 1, "IGN_ON": 2, "IGN_OFF": 3, "PLUG_IN": 4, "PLUG_OUT": 5, "HARSH_BRAKE": 6, "HARSH_ACCEL": 7, "DTC_RAISED": 8}
var chargeByName = map[string]int32{"IDLE": 1, "AC": 2, "DC_FAST": 3, "FAULT": 4}

func reject(raw []byte, reason canonical.Reason, format string, a ...any) canonical.Record {
	return canonical.Record{Raw: raw, Reject: canonical.Rejectf(reason, format, a...)}
}

// jsonRecords splits a JSON body into per-record raw messages.
func jsonRecords(body []byte, batch bool) ([][]byte, *canonical.Reject) {
	if !batch {
		return [][]byte{body}, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, canonical.Rejectf(canonical.Decode, "batch is not a JSON array: %v", err)
	}
	out := make([][]byte, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out, nil
}

// probeVersion applies the DECODE → SCHEMA → UNKNOWN_SCHEMA_VERSION precedence for JSON records.
func probeVersion(raw []byte, field string) *canonical.Reject {
	if !json.Valid(raw) {
		return canonical.Rejectf(canonical.Decode, "invalid JSON")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return canonical.Rejectf(canonical.Schema, "record is not an object")
	}
	v, ok := probe[field]
	if !ok {
		return canonical.Rejectf(canonical.Schema, "missing %s", field)
	}
	var n uint32
	if err := json.Unmarshal(v, &n); err != nil {
		return canonical.Rejectf(canonical.Schema, "%s is not an unsigned integer", field)
	}
	if n != canonical.SupportedSchema {
		return canonical.Rejectf(canonical.UnknownSchemaVersion, "%s %d", field, n)
	}
	return nil
}

// strictUnmarshal decodes an object, reporting type mismatches as SCHEMA.
func strictUnmarshal(raw []byte, v any) *canonical.Reject {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(v); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return canonical.Rejectf(canonical.Schema, "field %s: want %s", te.Field, te.Type)
		}
		return canonical.Rejectf(canonical.Decode, "%v", err)
	}
	return nil
}

// missing returns the first required field that is absent.
func missing(fields map[string]bool) string {
	for name, present := range fields {
		if !present {
			return name
		}
	}
	return ""
}
