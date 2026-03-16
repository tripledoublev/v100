package ui

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestCLIRendererUsesPlainSpeakerLabels(t *testing.T) {
	r := NewCLIRenderer()

	out := captureStdout(t, func() {
		r.RenderEvent(eventWithPayload(t, core.EventUserMsg, core.UserMsgPayload{
			Content: "hello",
		}))
		r.RenderEvent(eventWithPayload(t, core.EventModelResp, core.ModelRespPayload{
			Text: "world",
		}))
	})

	plain := stripANSI(out)
	if !strings.Contains(plain, " me  hello") {
		t.Fatalf("expected plain user label, got %q", plain)
	}
	if strings.Contains(plain, " you ") {
		t.Fatalf("did not expect old user label in %q", plain)
	}
	if !strings.Contains(plain, " agent  world") {
		t.Fatalf("expected plain agent label, got %q", plain)
	}
}

func TestCLIRendererPrintsNewlineBeforeFirstStreamedToken(t *testing.T) {
	r := NewCLIRenderer()
	r.modelCallStop = make(chan struct{})
	r.modelCallDone = make(chan struct{})
	close(r.modelCallDone)

	out := captureStdout(t, func() {
		r.RenderEvent(eventWithPayload(t, core.EventModelToken, map[string]string{
			"text": "streamed response",
		}))
	})

	if !strings.HasPrefix(out, "\n") {
		t.Fatalf("expected newline before streamed token, got %q", out)
	}
	if !strings.Contains(out, "streamed response") {
		t.Fatalf("expected streamed token output, got %q", out)
	}
}

func TestCLIRendererUsesPlainToolResultStatus(t *testing.T) {
	r := NewCLIRenderer()

	out := captureStdout(t, func() {
		r.RenderEvent(eventWithPayload(t, core.EventToolResult, core.ToolResultPayload{
			Name:       "sh",
			OK:         true,
			DurationMS: 12,
			Output:     "done",
		}))
	})

	plain := stripANSI(out)
	if !strings.Contains(plain, "ok sh  [12ms]  done") {
		t.Fatalf("expected plain tool result status, got %q", plain)
	}
	if strings.Contains(plain, "✓") {
		t.Fatalf("did not expect decorative success icon in %q", plain)
	}
}

func TestPromptWithImagesReadsPlainCLIInput(t *testing.T) {
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	defer func() {
		os.Stdin = old
		_ = r.Close()
	}()

	if _, err := w.WriteString("inspect this\n"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	got, err := PromptWithImages("")
	if err != nil {
		t.Fatalf("PromptWithImages returned error: %v", err)
	}
	if got.Text != "inspect this" {
		t.Fatalf("expected prompt text, got %q", got.Text)
	}
	if len(got.Images) != 0 {
		t.Fatalf("expected no images for plain stdin prompt, got %d", len(got.Images))
	}
}

func TestPromptLineIncludesImageCount(t *testing.T) {
	line := promptLine("check this", [][]byte{{0x89, 0x50, 0x4e, 0x47}}, "attached [Image #1]")
	if !strings.Contains(line, "check this") {
		t.Fatalf("expected prompt text in %q", line)
	}
	if !strings.Contains(line, "[Image #1]") {
		t.Fatalf("expected image marker in %q", line)
	}
}

func TestIsPNGData(t *testing.T) {
	if !isPNGData([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}) {
		t.Fatal("expected valid PNG signature to be accepted")
	}
	if isPNGData([]byte("plain text")) {
		t.Fatal("expected plain text to be rejected as PNG")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func eventWithPayload(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		TS:      time.Date(2026, 3, 14, 5, 5, 5, 0, time.UTC),
		Type:    typ,
		Payload: data,
	}
}
