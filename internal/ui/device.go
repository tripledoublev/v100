//go:build linux
// +build linux

package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Linux implementation

const powerSupplyDir = "/sys/class/power_supply"

func readDeviceStatus(now time.Time) deviceStatus {
	return readDeviceStatusFromDir(powerSupplyDir, now)
}

func readDeviceStatusFromDir(dir string, now time.Time) deviceStatus {
	status := deviceStatus{CheckedAt: now}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return status
	}
	for _, entry := range entries {
		base := filepath.Join(dir, entry.Name())
		if !isBatterySupply(base) {
			continue
		}
		status.BatteryPresent = true
		status.Percent = clampInt(readBatteryPercent(base), 0, 100)
		status.State = normalizeBatteryState(readTrimmedFile(filepath.Join(base, "status")))
		return status
	}
	return status
}

func isBatterySupply(base string) bool {
	typ := readTrimmedFile(filepath.Join(base, "type"))
	if strings.EqualFold(typ, "battery") {
		return true
	}
	if v := readIntFile(filepath.Join(base, "capacity")); v >= 0 {
		return true
	}
	if now, full := readIntFile(filepath.Join(base, "energy_now")), readIntFile(filepath.Join(base, "energy_full")); now >= 0 && full > 0 {
		return true
	}
	if now, full := readIntFile(filepath.Join(base, "charge_now")), readIntFile(filepath.Join(base, "charge_full")); now >= 0 && full > 0 {
		return true
	}
	return false
}

func readBatteryPercent(base string) int {
	if v := readIntFile(filepath.Join(base, "capacity")); v >= 0 {
		return v
	}
	now := readIntFile(filepath.Join(base, "energy_now"))
	full := readIntFile(filepath.Join(base, "energy_full"))
	if now >= 0 && full > 0 {
		return now * 100 / full
	}
	now = readIntFile(filepath.Join(base, "charge_now"))
	full = readIntFile(filepath.Join(base, "charge_full"))
	if now >= 0 && full > 0 {
		return now * 100 / full
	}
	return 0
}

func normalizeBatteryState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "charging":
		return "charging"
	case "discharging":
		return "battery"
	case "full":
		return "full"
	case "not charging":
		return "plugged"
	default:
		return ""
	}
}

func readTrimmedFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readIntFile(path string) int {
	s := readTrimmedFile(path)
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
