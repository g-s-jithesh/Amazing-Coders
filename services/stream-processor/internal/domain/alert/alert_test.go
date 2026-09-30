package alert

import (
	"regexp"
	"testing"
)

func TestSustainThenFireOnce(t *testing.T) {
	m := &Machine{ForMs: 30_000, ClearForM: 60_000}
	steps := []struct {
		ts     int64
		breach bool
		want   Transition
	}{
		{0, true, None},       // pending
		{10_000, true, None},  // still pending
		{20_000, false, None}, // cleared before For → back to OK, no alert
		{30_000, true, None},  // new pending episode starts at 30s
		{59_999, true, None},  // 29.999 s held
		{60_000, true, Fired}, // 30 s held → fire, window start 30s
		{70_000, true, None},  // no re-fire
		{80_000, false, None}, // clearing starts
		{90_000, true, None},  // breach again resets clearing
		{100_000, false, None},
		{159_999, false, None},
		{160_000, false, Resolved}, // clear held 60 s
		{170_000, false, None},
	}
	for i, s := range steps {
		if got := m.Step(s.ts, s.breach); got != s.want {
			t.Fatalf("step %d (ts=%d breach=%v): %v, want %v", i, s.ts, s.breach, got, s.want)
		}
	}
	if m.StartMs != 30_000 {
		t.Fatalf("episode start %d, want 30000", m.StartMs)
	}
}

func TestImmediateFireAndResolve(t *testing.T) {
	m := &Machine{}
	if m.Step(1, true) != Fired || m.Step(2, true) != None || m.Step(3, false) != Resolved || m.Phase != OK {
		t.Fatal("For=0/ClearFor=0 must fire and resolve immediately")
	}
}

func TestOlderObservationsIgnored(t *testing.T) {
	m := &Machine{}
	m.Step(100, true)
	if m.Step(50, false) != None || m.Phase != Firing {
		t.Fatal("an older observation must not change state")
	}
}

func TestIDDeterministicV5(t *testing.T) {
	a, b := ID("VIN", "THERMAL_OVERTEMP:critical", 123), ID("VIN", "THERMAL_OVERTEMP:critical", 123)
	if a != b {
		t.Fatal("same input must give the same id (redelivery idempotency)")
	}
	if a == ID("VIN", "THERMAL_OVERTEMP:critical", 124) || a == ID("VIN2", "THERMAL_OVERTEMP:critical", 123) {
		t.Fatal("different episodes must differ")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("not a UUIDv5: %s", a)
	}
}
