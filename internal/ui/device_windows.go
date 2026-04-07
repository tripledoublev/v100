//go:build windows
// +build windows

package ui

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Windows implementation using WMI queries

func readDeviceStatus(now time.Time) deviceStatus {
	status := deviceStatus{CheckedAt: now}

	// Try WMI query for battery status
	// wmic path Win32_Battery get EstimatedChargeRemaining, Status
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-WmiObject Win32_Battery | Select-Object EstimatedChargeRemaining,Status | ConvertTo-Csv -NoTypeInformation | Select-Object -Skip 1`)

	output, err := cmd.Output()
	if err != nil {
		return status
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse CSV: "84","2"  (percent, status)
		fields := strings.Split(line, ",")
		if len(fields) < 1 {
			continue
		}

		status.BatteryPresent = true

		// Remove quotes and parse percentage
		percentStr := strings.Trim(fields[0], "\"")
		if percent, err := strconv.Atoi(percentStr); err == nil {
			status.Percent = clampInt(percent, 0, 100)
		}

		// Parse status: 2=discharging, 4=charging, 3=critically low
		if len(fields) > 1 {
			statusStr := strings.Trim(fields[1], "\"")
			switch statusStr {
			case "2":
				status.State = "battery"
			case "4":
				status.State = "charging"
			case "1":
				status.State = "full"
			}
		}

		return status
	}

	return status
}
