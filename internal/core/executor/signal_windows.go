//go:build windows

package executor

import "os/exec"

type commandSignalTarget struct {
	cmd *exec.Cmd
}

func prepareCommand(cmd *exec.Cmd) {}

func newCommandSignalTarget(cmd *exec.Cmd) commandSignalTarget {
	return commandSignalTarget{cmd: cmd}
}

func terminateCommand(target commandSignalTarget) error {
	return killCommand(target)
}

func killCommand(target commandSignalTarget) error {
	if target.cmd.Process == nil {
		return nil
	}
	return target.cmd.Process.Kill()
}
