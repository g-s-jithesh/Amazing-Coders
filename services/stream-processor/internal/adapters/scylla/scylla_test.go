package scylla

import (
	"testing"

	gocql "github.com/apache/cassandra-gocql-driver/v2"

	"github.com/g-s-jithesh/Amazing-Coders/services/stream-processor/internal/domain/event"
)

// Absent groups and empty DTC lists must be unset (no tombstones), never nil or zero.
func TestArgsLeaveAbsentColumnsUnset(t *testing.T) {
	a := args(&event.Event{VIN: "V", TsEventMs: 1_788_238_800_000, Seq: 7})
	for _, i := range []int{5, 6, 7, 12, 13, 14, 15, 22} { // lat lon speed tmin tmax cmin cmax dtc
		if a[i] != gocql.UnsetValue {
			t.Errorf("column %d = %v, want UnsetValue", i, a[i])
		}
	}
	a = args(&event.Event{VIN: "V", Has: event.HasGPS | event.HasTemp | event.HasCells, Lat: 13, CellVMinMv: 3700, DTC: []string{"P0A7E"}})
	if a[5] != 13.0 || a[14] != 3700 || len(a[22].([]string)) != 1 {
		t.Fatalf("present groups: %v", a)
	}
	if len(a) != 24 {
		t.Fatalf("%d bind values for 24 columns", len(a))
	}
}
