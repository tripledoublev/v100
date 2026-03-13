package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const powerSupplyDir = "/sys/class/power_supply"

func readDeviceStatus(now time.Time) deviceStatus {
	status := deviceStatus{CheckedAt: now}
	entries, err := os.ReadDir(powerSupplyDir)
	if err != nil {
		return status
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		base := filepath.Join(powerSupplyDir, entry.Name())
		typ := readTrimmedFile(filepath.Join(base, "type"))
		if !strings.EqualFold(typ, "battery") {
			continue
		}
		status.BatteryPresent = true
		status.Percent = clampInt(readBatteryPercent(base), 0, 100)
		status.State = normalizeBatteryState(readTrimmedFile(filepath.Join(base, "status")))
		return status
	}
	return status
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

func (m *TUIModel) refreshDeviceStatus(now time.Time) {
	if !m.device.CheckedAt.IsZero() && time.Since(m.device.CheckedAt) < 30*time.Second {
		return
	}
	m.device = readDeviceStatus(now)
}

func (m *TUIModel) deviceStatusLine() string {
	if !m.device.BatteryPresent {
		return "device: no battery"
	}
	line := "device: "
	if m.device.State != "" {
		line += m.device.State + " "
	}
	line += strconv.Itoa(m.device.Percent) + "%"
	return line
}
