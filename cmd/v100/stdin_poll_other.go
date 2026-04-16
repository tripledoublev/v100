//go:build !linux

package main

import "time"

// stdinPollReady on non-Linux platforms falls back to a short sleep.
// The escape-key goroutine is only active when term.IsTerminal is true,
// which is rare in cross-compiled/non-interactive contexts.
func stdinPollReady(_ int) bool {
	time.Sleep(50 * time.Millisecond)
	return true
}
