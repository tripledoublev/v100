package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type processRunOutcome struct {
	res Result
	err error
}

func TestRunProcessSendsSIGTERMBeforeKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM is not available on windows")
	}
	restoreGrace := setProcessKillGraceForTest(500 * time.Millisecond)
	defer restoreGrace()

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	term := filepath.Join(dir, "term")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan processRunOutcome, 1)

	go func() {
		script := fmt.Sprintf(
			"trap 'printf term > %s; exit 7' TERM; printf ready > %s; while :; do :; done",
			strconv.Quote(term),
			strconv.Quote(ready),
		)
		res, err := runProcess(ctx, processRequest{Command: "sh", Args: []string{"-c", script}})
		done <- processRunOutcome{res: res, err: err}
	}()

	waitForFile(t, ready, 2*time.Second)
	cancel()

	outcome := waitForProcessRun(t, done, 2*time.Second)
	if outcome.err != nil {
		t.Fatalf("runProcess returned error: %v", outcome.err)
	}
	if outcome.res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7 (stderr=%q)", outcome.res.ExitCode, outcome.res.Stderr)
	}
	if outcome.res.Err == nil {
		t.Fatal("Result.Err is nil, want exit error")
	}
	if _, err := os.Stat(term); err != nil {
		t.Fatalf("SIGTERM trap did not write marker: %v", err)
	}
}

func TestRunProcessKillsAfterSIGTERMGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM is not available on windows")
	}
	restoreGrace := setProcessKillGraceForTest(25 * time.Millisecond)
	defer restoreGrace()

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan processRunOutcome, 1)

	go func() {
		script := fmt.Sprintf(
			"trap '' TERM; printf ready > %s; while :; do :; done",
			strconv.Quote(ready),
		)
		res, err := runProcess(ctx, processRequest{Command: "sh", Args: []string{"-c", script}})
		done <- processRunOutcome{res: res, err: err}
	}()

	waitForFile(t, ready, 2*time.Second)
	cancel()

	outcome := waitForProcessRun(t, done, 2*time.Second)
	if outcome.err != nil {
		t.Fatalf("runProcess returned error: %v", outcome.err)
	}
	if outcome.res.Err == nil {
		t.Fatal("Result.Err is nil, want forced-kill exit error")
	}
	if outcome.res.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want non-zero after forced kill")
	}
}

func TestRunProcessDrainsStdoutAndStderrOnExit(t *testing.T) {
	res, err := runProcess(context.Background(), processRequest{
		Command: "sh",
		Args: []string{"-c", `
i=0
while [ "$i" -lt 1000 ]; do
  printf 'out-%04d\n' "$i"
  printf 'err-%04d\n' "$i" >&2
  i=$((i + 1))
done
`},
	})
	if err != nil {
		t.Fatalf("runProcess returned error: %v", err)
	}
	if res.ExitCode != 0 || res.Err != nil {
		t.Fatalf("unexpected result: exit=%d err=%v stderr=%q", res.ExitCode, res.Err, res.Stderr)
	}
	if got := strings.Count(res.Stdout, "out-"); got != 1000 {
		t.Fatalf("stdout line count = %d, want 1000", got)
	}
	if got := strings.Count(res.Stderr, "err-"); got != 1000 {
		t.Fatalf("stderr line count = %d, want 1000", got)
	}
}

func TestRunProcessReturnsWhenBackgroundChildKeepsPipeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are not available on windows")
	}
	restoreGrace := setProcessKillGraceForTest(25 * time.Millisecond)
	defer restoreGrace()

	dir := t.TempDir()
	childPIDPath := filepath.Join(dir, "child.pid")
	start := time.Now()
	_, err := runProcess(context.Background(), processRequest{
		Command: "sh",
		Args: []string{"-c", fmt.Sprintf(
			"sleep 10 & printf '%%s' \"$!\" > %s; exit 0",
			strconv.Quote(childPIDPath),
		)},
	})
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("runProcess error = %v, want exec.ErrWaitDelay", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("runProcess took %s, want bounded pipe drain wait", elapsed)
	}

	pidBytes, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	waitForProcessExit(t, childPID, 2*time.Second)
}

func TestProcessPoolLimitsConcurrentRuns(t *testing.T) {
	restoreLimit := setProcessLimitForTest(1)
	defer restoreLimit()

	dir := t.TempDir()
	firstReady := filepath.Join(dir, "first-ready")
	releaseFirst := filepath.Join(dir, "release-first")
	secondStarted := filepath.Join(dir, "second-started")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstDone := make(chan processRunOutcome, 1)
	go func() {
		script := fmt.Sprintf(
			"printf ready > %s; while [ ! -f %s ]; do sleep 0.05; done",
			strconv.Quote(firstReady),
			strconv.Quote(releaseFirst),
		)
		res, err := runProcess(ctx, processRequest{Command: "sh", Args: []string{"-c", script}})
		firstDone <- processRunOutcome{res: res, err: err}
	}()
	waitForFile(t, firstReady, 2*time.Second)

	secondDone := make(chan processRunOutcome, 1)
	go func() {
		script := fmt.Sprintf("printf started > %s", strconv.Quote(secondStarted))
		res, err := runProcess(ctx, processRequest{Command: "sh", Args: []string{"-c", script}})
		secondDone <- processRunOutcome{res: res, err: err}
	}()

	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(secondStarted); err == nil {
		t.Fatal("second process started while first process held the only slot")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat second marker: %v", err)
	}

	if err := os.WriteFile(releaseFirst, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := waitForProcessRun(t, firstDone, 2*time.Second)
	second := waitForProcessRun(t, secondDone, 2*time.Second)
	for name, outcome := range map[string]processRunOutcome{"first": first, "second": second} {
		if outcome.err != nil {
			t.Fatalf("%s runProcess returned error: %v", name, outcome.err)
		}
		if outcome.res.ExitCode != 0 {
			t.Fatalf("%s ExitCode = %d, want 0", name, outcome.res.ExitCode)
		}
	}
}

func TestProcessPoolAcquireHonorsContext(t *testing.T) {
	restoreLimit := setProcessLimitForTest(1)
	defer restoreLimit()

	dir := t.TempDir()
	firstReady := filepath.Join(dir, "first-ready")
	releaseFirst := filepath.Join(dir, "release-first")
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer firstCancel()
	firstDone := make(chan processRunOutcome, 1)
	go func() {
		script := fmt.Sprintf(
			"printf ready > %s; while [ ! -f %s ]; do sleep 0.05; done",
			strconv.Quote(firstReady),
			strconv.Quote(releaseFirst),
		)
		res, err := runProcess(firstCtx, processRequest{Command: "sh", Args: []string{"-c", script}})
		firstDone <- processRunOutcome{res: res, err: err}
	}()
	waitForFile(t, firstReady, 2*time.Second)

	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer blockedCancel()
	_, err := runProcess(blockedCtx, processRequest{Command: "true"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runProcess error = %v, want context deadline exceeded", err)
	}

	if err := os.WriteFile(releaseFirst, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForProcessRun(t, firstDone, 2*time.Second)
}

func TestCurrentResourceStatsReportsFDLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("resource stats use /proc on linux")
	}
	stats, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}
	if stats.OpenFDs <= 0 {
		t.Fatalf("OpenFDs = %d, want positive", stats.OpenFDs)
	}
	if stats.FDSoftLimit <= 0 {
		t.Fatalf("FDSoftLimit = %d, want positive", stats.FDSoftLimit)
	}
	if stats.ProcessPoolLimit <= 0 {
		t.Fatalf("ProcessPoolLimit = %d, want positive", stats.ProcessPoolLimit)
	}
}

func TestCurrentResourceStatsCountsRunningSubprocess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("resource stats use /proc on linux")
	}
	before, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}

	cmd := exec.Command("sh", "-c", "sleep 2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := CurrentResourceStats()
		if err == nil && stats.RunningSubprocesses >= before.RunningSubprocesses+1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	stats, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}
	t.Fatalf("RunningSubprocesses = %d, want at least %d", stats.RunningSubprocesses, before.RunningSubprocesses+1)
}

func TestCurrentResourceStatsReportsProcessPoolExhaustion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("resource stats use /proc on linux")
	}
	restoreLimit := setProcessLimitForTest(1)
	defer restoreLimit()
	release, err := acquireProcessSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	stats, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}
	if stats.ProcessPoolUsed != 1 || stats.ProcessPoolLimit != 1 {
		t.Fatalf("process pool stats = %d/%d, want 1/1", stats.ProcessPoolUsed, stats.ProcessPoolLimit)
	}
	if warning := stats.ExhaustionWarning(); !strings.Contains(warning, "process pool exhausted") {
		t.Fatalf("ExhaustionWarning() = %q, want process pool exhausted", warning)
	}
}

func TestRunProcessStressNoZombieOrFDLeak(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("resource leak assertions use /proc on linux")
	}
	before, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}

	for i := 0; i < 1000; i++ {
		res, err := runProcess(context.Background(), processRequest{Command: "true"})
		if err != nil {
			t.Fatalf("run %d returned error: %v", i, err)
		}
		if res.ExitCode != 0 || res.Err != nil {
			t.Fatalf("run %d unexpected result: exit=%d err=%v", i, res.ExitCode, res.Err)
		}
	}

	after, err := CurrentResourceStats()
	if err != nil {
		t.Fatalf("CurrentResourceStats returned error: %v", err)
	}
	if after.ZombieSubprocesses > before.ZombieSubprocesses {
		t.Fatalf("zombies grew from %d to %d", before.ZombieSubprocesses, after.ZombieSubprocesses)
	}
	if delta := after.OpenFDs - before.OpenFDs; delta > 4 {
		t.Fatalf("open FDs grew by %d (%d to %d)", delta, before.OpenFDs, after.OpenFDs)
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForProcessRun(t *testing.T, done <-chan processRunOutcome, timeout time.Duration) processRunOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(timeout):
		t.Fatal("timed out waiting for process run")
		return processRunOutcome{}
	}
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists", pid)
}
