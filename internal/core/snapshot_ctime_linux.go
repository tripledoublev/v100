//go:build linux

package core

import (
	"os"
	"syscall"
)

func fileChangeTime(info os.FileInfo) (int64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Ctim.Nano(), true
}
