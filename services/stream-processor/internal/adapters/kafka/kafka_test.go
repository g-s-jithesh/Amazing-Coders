package kafka

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"

	alertsv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/alerts/v1"
	telemetryv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/telemetry/v1"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/app"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/rules"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/session"
	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/window"
)

func TestFromProtoPresence(t *testing.T) {
	m := &telemetryv1.TelemetryEvent{Vin: "V", SocPct: 50, Lat: proto.Float64(13), Lon: proto.Float64(80), SpeedKmh: proto.Float32(30),
		CellVMinMv: proto.Uint32(3700), CellVMaxMv: proto.Uint32(3710), ChargeState: telemetryv1.ChargeState_CHARGE_STATE_AC}
	e := FromProto(m)
	if e.Has != event.HasGPS|event.HasCells || e.Lat != 13 || e.CellVMaxMv != 3710 || e.PackTempMaxC != 0 || e.ChargeState != 2 {
		t.Fatalf("event %+v", e)
	}
}

func TestStateRoundTrip(t *testing.T) {
	s := app.StateOut{Latest: event.Event{VIN: "V", TsEventMs: 9, Lat: 1, Lon: 2, SpeedKmh: 3, SoCPct: 4, PackTempMaxC: 5, ChargeState: 2},
		Firing: []string{"THERMAL_OVERTEMP|critical|"},
		Rules: rules.Snapshot{LastTsMs: 9, IgnOn: true, DeltaMean: 1.5, DeltaVar: 0.2, DeltaN: 7, ActiveDTC: []string{"P0A7E"},
			Machines: []rules.MachineSnap{{Key: "THERMAL_OVERTEMP|critical|", Phase: 2, StartMs: 1, ClearMs: 0, LastMs: 9}}}}
	b, _ := proto.Marshal(stateProto(s))
	var m telemetryv1.VehicleState
	if err := proto.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if got := StateFromProto(&m); !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, s)
	}
}

func TestAlertSessionRollupMapping(t *testing.T) {
	a := alertProto(app.AlertOut{Alert: rules.Alert{ID: "x", VIN: "V", RuleID: "DTC_RAISED", Severity: "medium", State: "RESOLVED", Detail: "U0111"}, TenantID: "T"})
	if a.Severity != alertsv1.Severity_SEVERITY_WARNING || a.State != alertsv1.AlertState_ALERT_STATE_RESOLVED || a.Detail != "U0111" || a.TenantId != "T" {
		t.Fatalf("alert %+v", a)
	}
	s := sessionProto(app.SessionOut{Session: session.Session{Kind: session.Drive, Phase: "END", DistanceKm: 3}})
	if s.Kind.String() != "KIND_DRIVE" || s.Phase.String() != "PHASE_END" || s.VRestBefore != nil {
		t.Fatalf("session %+v", s)
	}
	s = sessionProto(app.SessionOut{Session: session.Session{Kind: session.Charge, HasRestBefore: true, VRestBefore: 351}})
	if s.GetVRestBefore() != 351 {
		t.Fatal("rest voltage lost")
	}
	r := rollupProto(window.Rollup{VIN: "V", HasTemp: true, TempMaxC: 40})
	if r.TempMaxC == nil || *r.TempMaxC != 40 || r.ImbalanceMaxMv != nil {
		t.Fatalf("rollup presence %+v", r)
	}
}
