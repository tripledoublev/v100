package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultMaxConcurrentProcesses = 64

var (
	processLimiterMu sync.Mutex
	processLimiter   = make(chan struct{}, defaultMaxConcurrentProcesses)

	processKillGrace = 2 * time.Second
)

type processRequest struct {
	Command      string
	Args         []string
	Env          []string
	Dir          string
	Stdin        io.Reader
	StdoutWriter io.Writer
	StderrWriter io.Writer
}

func processPoolStats() (int, int) {
	processLimiterMu.Lock()
	defer processLimiterMu.Unlock()
	return len(processLimiter), cap(processLimiter)
}

func runProcess(ctx context.Context, req processRequest) (Result, error) {
	if strings.TrimSpace(req.Command) == "" {
		return Result{}, fmt.Errorf("executor: command is required")
	}
	release, err := acquireProcessSlot(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()

	cmd := exec.Command(req.Command, req.Args...)
	prepareCommand(cmd)
	cmd.Dir = req.Dir
	if req.Env != nil {
		cmd.Env = req.Env
	}
	if req.Stdin != nil {
		cmd.Stdin = req.Stdin
	}
	cmd.WaitDelay = processKillGrace

	var stdout, stderr bytes.Buffer
	var stdoutW io.Writer = &stdout
	var stderrW io.Writer = &stderr
	if req.StdoutWriter != nil {
		stdoutW = io.MultiWriter(stdoutW, req.StdoutWriter)
	}
	if req.StderrWriter != nil {
		stderrW = io.MultiWriter(stderrW, req.StderrWriter)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	target := newCommandSignalTarget(cmd)

	waitErr := waitForProcess(ctx, cmd, target)
	exitCode, resultErr, err := classifyProcessError(waitErr, ctx.Err())
	if err != nil {
		return Result{}, err
	}
	return Result{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      resultErr,
	}, nil
}

func acquireProcessSlot(ctx context.Context) (func(), error) {
	processLimiterMu.Lock()
	limiter := processLimiter
	processLimiterMu.Unlock()

	select {
	case limiter <- struct{}{}:
		return func() { <-limiter }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func waitForProcess(ctx context.Context, cmd *exec.Cmd, target commandSignalTarget) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if errors.Is(err, exec.ErrWaitDelay) {
			_ = killCommand(target)
		}
		return err
	case <-ctx.Done():
		_ = terminateCommand(target)
		select {
		case err := <-done:
			if errors.Is(err, exec.ErrWaitDelay) {
				_ = killCommand(target)
			}
			return err
		case <-time.After(processKillGrace):
			_ = killCommand(target)
			select {
			case err := <-done:
				if errors.Is(err, exec.ErrWaitDelay) {
					_ = killCommand(target)
				}
				return err
			case <-time.After(processKillGrace + time.Second):
				return ctx.Err()
			}
		}
	}
}

func classifyProcessError(waitErr error, ctxErr error) (int, error, error) {
	if waitErr == nil {
		if ctxErr != nil {
			return -1, ctxErr, nil
		}
		return 0, nil, nil
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode(), waitErr, nil
	}
	if ctxErr != nil {
		return -1, ctxErr, nil
	}
	return 0, nil, waitErr
}

func setProcessLimitForTest(max int) func() {
	if max <= 0 {
		panic("process limit must be positive")
	}
	processLimiterMu.Lock()
	oldLimiter := processLimiter
	processLimiter = make(chan struct{}, max)
	processLimiterMu.Unlock()

	return func() {
		processLimiterMu.Lock()
		processLimiter = oldLimiter
		processLimiterMu.Unlock()
	}
}

func setProcessKillGraceForTest(d time.Duration) func() {
	old := processKillGrace
	processKillGrace = d
	return func() { processKillGrace = old }
}
