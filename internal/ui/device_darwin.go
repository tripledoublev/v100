// +build darwin

package ui

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// macOS implementation using pmset

func readDeviceStatus(now time.Time) deviceStatus {
	status := deviceStatus{CheckedAt: now}

	// Try to read battery info from pmset
	cmd := exec.Command("pmset", "-g", "batt")
	output, err := cmd.Output()
	if err != nil {
		return status
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "%") {
			continue
		}

		// Parse percentage: "Internal Battery	84%; charging; 2:45 remaining"
		percentRe := regexp.MustCompile(`(\d+)%`)
		matches := percentRe.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}

		status.BatteryPresent = true
		percent, _ := strconv.Atoi(matches[1])
		status.Percent = clampInt(percent, 0, 100)

		// Parse state
		if strings.Contains(line, "charging") {
			status.State = "charging"
		} else if strings.Contains(line, "discharging") {
			status.State = "battery"
		} else if strings.Contains(line, "AC Power") {
			status.State = "plugged"
		} else if strings.Contains(line, "charged") {
			status.State = "full"
		}

		return status
	}

	return status
}
