//go:build linux

package main

import "syscall"

// stdinPollReady reports whether fd has data ready to read within 50 ms.
// Uses syscall.Select so the escape-key goroutine never blocks longer than
// that window, allowing ConfirmTool to acquire stdin without racing.
func stdinPollReady(fd int) bool {
	tv := syscall.Timeval{Usec: 50_000}
	rset := new(syscall.FdSet)
	rset.Bits[fd/64] |= 1 << (uint(fd) % 64)
	n, err := syscall.Select(fd+1, rset, nil, nil, &tv)
	return err == nil && n > 0
}
