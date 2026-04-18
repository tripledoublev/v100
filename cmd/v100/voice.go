package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// captureVoiceInput invokes the v100-listen shim, returning the transcript.
// Stderr from the shim streams directly to the user so they see "listening"/"transcribing".
func captureVoiceInput() (string, error) {
	bin := os.Getenv("V100_LISTEN_CMD")
	if bin == "" {
		bin = "v100-listen"
	}
	cmd := exec.Command(bin)
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			return "", nil
		}
		return "", fmt.Errorf("%s: %w", bin, err)
	}
	return strings.TrimSpace(out.String()), nil
}

// isStopVoicePhrase recognizes utterances that should exit interactive voice mode.
func isStopVoicePhrase(s string) bool {
	norm := strings.ToLower(strings.TrimSpace(s))
	norm = strings.TrimRight(norm, ".!?, ")
	switch norm {
	case "stop voice", "exit voice", "voice off", "stop voice mode", "exit voice mode", "quit voice":
		return true
	}
	return false
}

