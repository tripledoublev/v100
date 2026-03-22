package core

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WakeStatus represents the current state of a wake process.
type WakeStatus string

const (
	WakeStatusIdle    WakeStatus = "idle"
	WakeStatusRunning WakeStatus = "running"
	WakeStatusStopped WakeStatus = "stopped"
	WakeStatusFailed  WakeStatus = "failed"
)

// WakeState is the persistent state of a v100 wake daemon.
type WakeState struct {
	Status     WakeStatus `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
	PID        int        `json:"pid,omitempty"`
	Token      string     `json:"token,omitempty"`
	Executable string     `json:"executable,omitempty"`

	IntervalSeconds int        `json:"interval_seconds"`
	NextRunAt       time.Time  `json:"next_run_at"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastRunID       string     `json:"last_run_id,omitempty"`

	ConsecutiveFailures int        `json:"consecutive_failures"`
	BackoffUntil        *time.Time `json:"backoff_until,omitempty"`

	QueuedGoals []GeneratedGoal `json:"queued_goals,omitempty"`

	Provider string `json:"provider"`
	Solver   string `json:"solver,omitempty"`
}

// DefaultWakeStatePath returns the default path for the wake state file.
// Uses XDG config directory: $XDG_CONFIG_HOME/v100/wake.json or ~/.config/v100/wake.json.
func DefaultWakeStatePath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "v100", "wake.json")
	}
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "v100")
	return filepath.Join(configDir, "wake.json")
}

// DefaultWakeLogPath returns the default path for the wake daemon log file.
func DefaultWakeLogPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "v100", "wake.log")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "v100", "wake.log")
}

// InitWakeState returns a new idle WakeState.
func InitWakeState() *WakeState {
	return &WakeState{
		Status:              WakeStatusIdle,
		IntervalSeconds:     3600,
		NextRunAt:           time.Now(),
		ConsecutiveFailures: 0,
		QueuedGoals:         []GeneratedGoal{},
	}
}

// NewWakeToken returns a random token used to verify wake daemon ownership.
func NewWakeToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate wake token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ReadWakeState reads a WakeState from disk.
func ReadWakeState(path string) (*WakeState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("wake state not found at %s", path)
		}
		return nil, fmt.Errorf("read wake state: %w", err)
	}

	var state WakeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal wake state: %w", err)
	}

	return &state, nil
}

// WriteWakeState writes a WakeState to disk atomically (write to .tmp, then rename).
func WriteWakeState(path string, s *WakeState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for wake state: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wake state: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write wake state tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename wake state tmp: %w", err)
	}

	return nil
}

// WakeCycleDelay returns the delay until the next wake cycle.
// Uses exponential backoff on failures: delay = min(interval * 2^failures, maxBackoff) ± 25% jitter.
func WakeCycleDelay(intervalSecs, maxBackoffSecs, failures int) time.Duration {
	if failures <= 0 {
		return time.Duration(intervalSecs) * time.Second
	}

	// Exponential backoff: interval * 2^failures
	delayFloat := float64(intervalSecs) * math.Pow(2, float64(failures))

	// Cap at maxBackoff
	if delayFloat > float64(maxBackoffSecs) {
		delayFloat = float64(maxBackoffSecs)
	}

	// Add ±25% jitter
	jitterRange := delayFloat * 0.25
	jitter := jitterRange * (mathrand.Float64()*2 - 1) //nolint:gosec
	delayFloat += jitter

	// Ensure non-negative
	if delayFloat < 0 {
		delayFloat = 0
	}

	return time.Duration(delayFloat) * time.Second
}

// WakeProcessExists reports whether the PID currently exists.
func WakeProcessExists(pid int) bool {
	if !WakeOwnershipSupported() {
		return false
	}
	if pid <= 0 {
		return false
	}
	_, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	return err == nil
}

// WakeOwnershipSupported reports whether PID ownership checks are supported.
func WakeOwnershipSupported() bool {
	return runtime.GOOS == "linux"
}

// WakeProcessOwned reports whether the PID matches the recorded executable and token.
func WakeProcessOwned(state *WakeState) bool {
	if !WakeOwnershipSupported() {
		return false
	}
	if state == nil || state.PID <= 0 || state.Executable == "" || state.Token == "" {
		return false
	}

	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", state.PID))
	if err != nil {
		return false
	}
	if filepath.Clean(exe) != filepath.Clean(state.Executable) {
		return false
	}

	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", state.PID))
	if err != nil {
		return false
	}
	return bytes.Contains(cmdline, []byte("wake")) &&
		bytes.Contains(cmdline, []byte("run")) &&
		bytes.Contains(cmdline, []byte(state.Token))
}
