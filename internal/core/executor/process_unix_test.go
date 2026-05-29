//go:build !windows

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunProcessTerminatesChildProcessGroup(t *testing.T) {
	restoreGrace := setProcessKillGraceForTest(100 * time.Millisecond)
	defer restoreGrace()

	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan processRunOutcome, 1)

	go func() {
		script := fmt.Sprintf(
			"sleep 10 & printf '%%s' \"$!\" > %s; wait",
			strconv.Quote(childPIDPath),
		)
		res, err := runProcess(ctx, processRequest{Command: "sh", Args: []string{"-c", script}})
		done <- processRunOutcome{res: res, err: err}
	}()

	waitForFile(t, childPIDPath, 2*time.Second)
	pidBytes, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	if !processExists(childPID) {
		t.Fatalf("child process %d was not running before cancellation", childPID)
	}

	cancel()
	outcome := waitForProcessRun(t, done, 2*time.Second)
	if outcome.err != nil {
		t.Fatalf("runProcess returned error: %v", outcome.err)
	}
	if outcome.res.Err == nil {
		t.Fatal("Result.Err is nil, want signal exit error")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processExists(childPID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d still exists after parent cancellation", childPID)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
