//go:build windows

package executor

func processExists(pid int) bool {
	return false
}
