package ui

import "testing"

func TestNormalizeBatteryState(t *testing.T) {
	cases := map[string]string{
		"Charging":     "charging",
		"Discharging":  "battery",
		"Full":         "full",
		"Not charging": "plugged",
		"Unknown":      "",
	}
	for in, want := range cases {
		if got := normalizeBatteryState(in); got != want {
			t.Fatalf("normalizeBatteryState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeviceStatusLine(t *testing.T) {
	m := NewTUIModel()
	m.device = deviceStatus{}
	if got := m.deviceStatusLine(); got != "device: no battery" {
		t.Fatalf("deviceStatusLine() = %q, want no-battery fallback", got)
	}

	m.device = deviceStatus{BatteryPresent: true, Percent: 84, State: "charging"}
	if got := m.deviceStatusLine(); got != "device: charging 84%" {
		t.Fatalf("deviceStatusLine() = %q, want charging line", got)
	}

	m.device = deviceStatus{BatteryPresent: true, Percent: 100, State: "full"}
	if got := m.deviceStatusLine(); got != "device: full 100%" {
		t.Fatalf("deviceStatusLine() = %q, want full line", got)
	}
}
