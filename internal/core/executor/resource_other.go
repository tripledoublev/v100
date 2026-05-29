//go:build !linux

package executor

import "fmt"

// ResourceStats describes executor-relevant process resources for the current v100 process.
type ResourceStats struct {
	OpenFDs             int
	FDSoftLimit         uint64
	RunningSubprocesses int
	ZombieSubprocesses  int
	ProcessPoolUsed     int
	ProcessPoolLimit    int
}

// CurrentResourceStats reports resource stats when the platform exposes the needed data.
func CurrentResourceStats() (ResourceStats, error) {
	return ResourceStats{}, fmt.Errorf("executor resource stats are unsupported on this platform")
}

func (s ResourceStats) ExhaustionWarning() string {
	return ""
}
