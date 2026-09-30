package telemetry

import "testing"

func TestNames(t *testing.T) {
	if EvtIgnOn.String() != "IGN_ON" || EvtDTCRaised.String() != "DTC_RAISED" || EventType(42).String() != "UNSPECIFIED" || EventType(-1).String() != "UNSPECIFIED" {
		t.Error("event names")
	}
	if ChargeDCFast.String() != "DC_FAST" || ChargeState(9).String() != "UNSPECIFIED" || ChargeState(-1).String() != "UNSPECIFIED" {
		t.Error("charge names")
	}
}
