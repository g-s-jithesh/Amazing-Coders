package mqtt

import "testing"

func TestParseTopic(t *testing.T) {
	for topic, want := range map[string][2]string{
		"v1/oem_a/0KACV21L0MB000139/telemetry": {"oem_a", "0KACV21L0MB000139"},
		"v1/oem_c/0KCDV45N9RC000001/t":         {"oem_c", "0KCDV45N9RC000001"},
	} {
		oem, vin, ok := ParseTopic(topic)
		if !ok || oem != want[0] || vin != want[1] {
			t.Errorf("ParseTopic(%s) = %s %s %v", topic, oem, vin, ok)
		}
	}
	for _, bad := range []string{"v2/oem_a/VIN/t", "v1/oem_a", "v1//VIN/t", "v1/oem_a//t", ""} {
		if _, _, ok := ParseTopic(bad); ok {
			t.Errorf("ParseTopic(%q) accepted", bad)
		}
	}
}
