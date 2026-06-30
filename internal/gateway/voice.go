package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	VoiceReplyModeAudio     = "audio"
	VoiceReplyModeAudioText = "audio+text"
	defaultVoiceReplyMode   = VoiceReplyModeAudioText
)

var errSynthesizerUnavailable = errors.New("no TTS command configured")

// synthesizeReply runs the configured TTS shim and returns the generated audio
// path. The shim receives reply text on stdin and writes the audio file path to
// stdout. V100_TTS_CMD may include arguments or shell syntax.
func synthesizeReply(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("reply text is required")
	}
	raw := strings.TrimSpace(os.Getenv("V100_TTS_CMD"))

	var cmd *exec.Cmd
	if raw == "" {
		if _, err := exec.LookPath("v100-tts"); err != nil {
			return "", errSynthesizerUnavailable
		}
		cmd = exec.CommandContext(ctx, "v100-tts")
	} else if strings.ContainsAny(raw, "|<>&;") {
		cmd = exec.CommandContext(ctx, "sh", "-c", raw)
	} else {
		parts := strings.Fields(raw)
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	}

	var out, errBuf bytes.Buffer
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tts: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	audioPath := strings.TrimSpace(out.String())
	if audioPath == "" {
		return "", fmt.Errorf("tts produced no audio path")
	}
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("tts audio path %q: %w", audioPath, err)
	}
	return audioPath, nil
}

func normalizeVoiceReplyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case VoiceReplyModeAudio:
		return VoiceReplyModeAudio
	case VoiceReplyModeAudioText, "":
		return VoiceReplyModeAudioText
	default:
		return VoiceReplyModeAudioText
	}
}
