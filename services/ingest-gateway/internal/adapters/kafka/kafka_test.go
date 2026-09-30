package kafka

import (
	"testing"

	"google.golang.org/protobuf/proto"

	telemetryv1 "github.com/g-s-jithesh/Amazing-Coders/libs/proto/gen/go/kilowatt/telemetry/v1"
	"github.com/g-s-jithesh/Amazing-Coders/services/ingest-gateway/internal/domain/canonical"
)

// Absent sensor groups must stay unset on the wire, so consumers can tell "missing" from zero.
func TestToProtoPresence(t *testing.T) {
	e := canonical.Event{VIN: "V", Lat: 13, CellVMinMv: 3700, Has: canonical.HasGPS | canonical.HasCells, ChargeState: 2, Evt: 4, SchemaVersion: 1}
	b, err := ProtoEncoder{}.Marshal(&e)
	if err != nil {
		t.Fatal(err)
	}
	var m telemetryv1.TelemetryEvent
	if err := proto.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Lat == nil || m.GetLat() != 13 || m.PackTempMaxC != nil || m.CellVMinMv == nil || m.GetCellVMinMv() != 3700 {
		t.Fatalf("presence wrong: %v", &m)
	}
	if m.ChargeState != telemetryv1.ChargeState_CHARGE_STATE_AC || m.Evt != telemetryv1.EventType_EVENT_TYPE_PLUG_IN || m.Vin != "V" {
		t.Fatalf("enums/fields wrong: %v", &m)
	}
}
