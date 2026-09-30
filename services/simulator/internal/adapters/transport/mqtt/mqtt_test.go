package mqtt

import "testing"

func TestTopic(t *testing.T) {
	for oem, want := range map[string]string{"oem_a": "v1/oem_a/VIN/telemetry", "oem_c": "v1/oem_c/VIN/t"} {
		if got, err := Topic(oem, "VIN"); err != nil || got != want {
			t.Errorf("Topic(%s) = %q, %v", oem, got, err)
		}
	}
	if _, err := Topic("oem_b", "VIN"); err == nil {
		t.Error("oem_b is HTTPS-only")
	}
}
