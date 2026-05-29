//go:build !windows

package executor

import (
	"os/exec"
	"syscall"
)

type commandSignalTarget struct {
	cmd      *exec.Cmd
	pgid     int
	hasPGID  bool
	fallback bool
}

func prepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func newCommandSignalTarget(cmd *exec.Cmd) commandSignalTarget {
	target := commandSignalTarget{cmd: cmd}
	if cmd.Process == nil {
		return target
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		target.pgid = pgid
		target.hasPGID = true
	}
	target.fallback = true
	return target
}

func terminateCommand(target commandSignalTarget) error {
	return signalCommandGroup(target, syscall.SIGTERM)
}

func killCommand(target commandSignalTarget) error {
	return signalCommandGroup(target, syscall.SIGKILL)
}

func signalCommandGroup(target commandSignalTarget, signal syscall.Signal) error {
	if target.hasPGID {
		if err := syscall.Kill(-target.pgid, signal); err == nil {
			return nil
		}
	}
	if target.fallback && target.cmd.Process != nil {
		return target.cmd.Process.Signal(signal)
	}
	return nil
}
