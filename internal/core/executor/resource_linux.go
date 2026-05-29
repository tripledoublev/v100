//go:build linux

package executor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// ResourceStats describes executor-relevant process resources for the current v100 process.
type ResourceStats struct {
	OpenFDs             int
	FDSoftLimit         uint64
	RunningSubprocesses int
	ZombieSubprocesses  int
	ProcessPoolUsed     int
	ProcessPoolLimit    int
}

// CurrentResourceStats reports open file descriptors and direct child process state.
func CurrentResourceStats() (ResourceStats, error) {
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return ResourceStats{}, fmt.Errorf("read /proc/self/fd: %w", err)
	}
	running, zombies, err := countDirectChildren(os.Getpid())
	if err != nil {
		return ResourceStats{}, err
	}

	poolUsed, poolLimit := processPoolStats()
	stats := ResourceStats{
		OpenFDs:             len(fds),
		RunningSubprocesses: running,
		ZombieSubprocesses:  zombies,
		ProcessPoolUsed:     poolUsed,
		ProcessPoolLimit:    poolLimit,
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
		stats.FDSoftLimit = limit.Cur
	}
	return stats, nil
}

func (s ResourceStats) ExhaustionWarning() string {
	switch {
	case s.ZombieSubprocesses > 0:
		return fmt.Sprintf("%d zombie subprocess(es)", s.ZombieSubprocesses)
	case s.ProcessPoolLimit > 0 && s.ProcessPoolUsed >= s.ProcessPoolLimit:
		return fmt.Sprintf("executor process pool exhausted (%d/%d)", s.ProcessPoolUsed, s.ProcessPoolLimit)
	case s.FDSoftLimit > 0 && uint64(s.OpenFDs)*100 >= s.FDSoftLimit*80:
		return fmt.Sprintf("open file descriptors near soft limit (%d/%d)", s.OpenFDs, s.FDSoftLimit)
	default:
		return ""
	}
}

func countDirectChildren(parentPID int) (int, int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, 0, fmt.Errorf("read /proc: %w", err)
	}

	running := 0
	zombies := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		state, ppid, err := readProcStat(entry.Name())
		if err != nil {
			continue
		}
		if ppid != parentPID {
			continue
		}
		if state == "Z" {
			zombies++
		} else {
			running++
		}
	}
	return running, zombies, nil
}

func readProcStat(pid string) (string, int, error) {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return "", 0, err
	}
	stat := string(b)
	endComm := strings.LastIndex(stat, ")")
	if endComm == -1 || endComm+2 >= len(stat) {
		return "", 0, fmt.Errorf("malformed stat for pid %s", pid)
	}
	fields := strings.Fields(stat[endComm+2:])
	if len(fields) < 2 {
		return "", 0, fmt.Errorf("malformed stat fields for pid %s", pid)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, err
	}
	return fields[0], ppid, nil
}
