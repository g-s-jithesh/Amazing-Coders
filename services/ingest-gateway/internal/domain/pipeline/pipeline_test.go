package pipeline

import (
	"testing"

	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

const recv = 1_788_238_800_000

var wmi = map[string]string{"0KA": "oem_a", "0KB": "oem_b", "0KC": "oem_c"}

func good() canonical.Event {
	return canonical.Event{
		VIN: "0KCDV45N9RC000001", TsEventMs: recv - 1000, Seq: 1, SoCPct: 60, PackVoltageV: 360,
		IsolationKohm: 2500, Aux12vV: 13.9, AmbientC: 31, ChargeState: 1, Evt: 1, SchemaVersion: 1,
		DTC: []string{"P0A7E", "U0111"},
	}
}

func meta() *Meta { return &Meta{OEM: "oem_c", TopicVIN: "0KCDV45N9RC000001", ReceivedMs: recv} }

func TestEachReason(t *testing.T) {
	chain := Default(wmi)
	cases := []struct {
		name   string
		mutate func(*Meta, *canonical.Event)
		want   canonical.Reason
	}{
		{"schema", func(_ *Meta, e *canonical.Event) { e.SchemaVersion = 99 }, canonical.UnknownSchemaVersion},
		{"vin short", func(m *Meta, e *canonical.Event) { e.VIN, m.TopicVIN = "0KCDV45N9RC00001", "" }, canonical.VINFormat},
		{"vin has O", func(m *Meta, e *canonical.Event) { e.VIN, m.TopicVIN = "0KCDV45N9RC00000O", "" }, canonical.VINFormat},
		{"vin lowercase", func(m *Meta, e *canonical.Event) { e.VIN, m.TopicVIN = "0kcdv45n9rc000001", "" }, canonical.VINFormat},
		{"vin checksum", func(m *Meta, e *canonical.Event) { e.VIN, m.TopicVIN = "0KCDV45N7RC000001", "" }, canonical.VINChecksum},
		{"topic ≠ payload", func(m *Meta, _ *canonical.Event) { m.TopicVIN = "0KCDV45N9RC000002" }, canonical.IdentityMismatch},
		{"wrong oem for wmi", func(m *Meta, _ *canonical.Event) { m.OEM = "oem_a" }, canonical.IdentityMismatch},
		{"unregistered wmi", func(m *Meta, e *canonical.Event) {
			e.VIN, m.TopicVIN = "1M8GDM9AXKP042788", "" // real-world-format VIN, not one of our synthetic WMIs
		}, canonical.IdentityMismatch},
		{"dtc", func(_ *Meta, e *canonical.Event) { e.DTC = []string{"P0A7E", "PX12Z"} }, canonical.DTCFormat},
		{"range", func(_ *Meta, e *canonical.Event) { e.SoCPct = 250 }, canonical.Range},
	}
	for _, c := range cases {
		m, e := meta(), good()
		c.mutate(m, &e)
		if r := chain.Run(m, &e); r == nil || r.Reason != c.want {
			t.Errorf("%s: got %v, want %s", c.name, r, c.want)
		}
	}
	m, e := meta(), good()
	if r := chain.Run(m, &e); r != nil {
		t.Fatalf("good record rejected: %v", r)
	}
	m.TopicVIN = "" // HTTPS batches carry no topic VIN
	if r := chain.Run(m, &e); r != nil {
		t.Fatalf("HTTPS record rejected: %v", r)
	}
}

// Order matters: a record with several faults reports the earliest step's reason.
func TestFirstRejectionWins(t *testing.T) {
	m, e := meta(), good()
	e.SchemaVersion, e.SoCPct, e.DTC = 2, 250, []string{"bad"}
	if r := Default(wmi).Run(m, &e); r.Reason != canonical.UnknownSchemaVersion {
		t.Fatalf("got %v", r.Reason)
	}
	e.SchemaVersion = 1
	if r := Default(wmi).Run(m, &e); r.Reason != canonical.DTCFormat {
		t.Fatalf("got %v", r.Reason)
	}
}

func TestStepNamesAreUniqueAndOrdered(t *testing.T) {
	want := []string{"schema_version", "vin_format", "vin_checksum", "identity", "dtc_format", "range"}
	chain := Default(wmi)
	if len(chain) != len(want) {
		t.Fatalf("%d steps", len(chain))
	}
	for i, s := range chain {
		if s.Name != want[i] {
			t.Fatalf("step %d = %s, want %s", i, s.Name, want[i])
		}
	}
}
