package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
	m := NewTUIModel(false, false)
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

func TestReadDeviceStatusFromDirDetectsSymlinkedBattery(t *testing.T) {
	root := t.TempDir()
	realBattery := filepath.Join(root, "real-battery")
	if err := os.MkdirAll(realBattery, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBatteryFile(t, realBattery, "type", "Battery\n")
	writeBatteryFile(t, realBattery, "capacity", "84\n")
	writeBatteryFile(t, realBattery, "status", "Charging\n")
	if err := os.Symlink(realBattery, filepath.Join(root, "BAT0")); err != nil {
		t.Fatal(err)
	}

	got := readDeviceStatusFromDir(root, time.Unix(1700000000, 0))
	if !got.BatteryPresent {
		t.Fatal("expected battery to be detected through symlinked power_supply entry")
	}
	if got.Percent != 84 {
		t.Fatalf("Percent = %d, want 84", got.Percent)
	}
	if got.State != "charging" {
		t.Fatalf("State = %q, want charging", got.State)
	}
}

func TestReadDeviceStatusFromDirFallsBackWithoutTypeFile(t *testing.T) {
	root := t.TempDir()
	battery := filepath.Join(root, "BAT1")
	if err := os.MkdirAll(battery, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBatteryFile(t, battery, "capacity", "62\n")
	writeBatteryFile(t, battery, "status", "Discharging\n")

	got := readDeviceStatusFromDir(root, time.Unix(1700000000, 0))
	if !got.BatteryPresent {
		t.Fatal("expected battery-like entry with capacity file to be detected")
	}
	if got.Percent != 62 {
		t.Fatalf("Percent = %d, want 62", got.Percent)
	}
	if got.State != "battery" {
		t.Fatalf("State = %q, want battery", got.State)
	}
}

func writeBatteryFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
