package ui

import (
	"strings"
	"testing"
	"time"
)

func TestLiveMetricDashboardIncludesVelocitySignals(t *testing.T) {
	out := LiveMetricDashboard(3, 10, 1200, 8000, 700, 500, 0.01, 1.0, 2400, 2, 4, 7, 1, "thinking", 3*time.Second, 48)
	for _, want := range []string{
		"visual inspector",
		"velocity:",
		"model:4/30s",
		"tools:7/30s",
		"compress:1/30s",
		"health:",
		"state:",
		"last step:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q in:\n%s", want, out)
		}
	}
}
