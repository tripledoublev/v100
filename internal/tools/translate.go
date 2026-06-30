package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

const translateMaxInputBytes = 8 * 1024

type translateTool struct{}

type translateArgs struct {
	Text       string `json:"text"`
	TargetLang string `json:"target_lang"`
	SourceLang string `json:"source_lang"`
	Formality  string `json:"formality"`
}

type translateOutput struct {
	Translated string `json:"translated"`
	SourceLang string `json:"source_lang,omitempty"`
	TargetLang string `json:"target_lang"`
}

func Translate() Tool { return &translateTool{} }

func (t *translateTool) Name() string { return "translate" }
func (t *translateTool) Description() string {
	return "Translate text to a target language using V100_TRANSLATE_CMD when configured, otherwise the active model provider."
}
func (t *translateTool) DangerLevel() DangerLevel { return Safe }
func (t *translateTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *translateTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["text", "target_lang"],
		"properties": {
			"text": {"type": "string", "description": "Text to translate. Maximum 8192 bytes."},
			"target_lang": {"type": "string", "description": "Target language code or name, e.g. fr, en, French."},
			"source_lang": {"type": "string", "description": "Optional source language code or name. Empty means auto-detect."},
			"formality": {"type": "string", "enum": ["default", "informal", "formal"], "description": "Optional register preference.", "default": "default"}
		}
	}`)
}

func (t *translateTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"translated": {"type": "string"},
			"source_lang": {"type": "string"},
			"target_lang": {"type": "string"}
		},
		"required": ["translated", "target_lang"]
	}`)
}

func (t *translateTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a translateArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	a.Text = strings.TrimSpace(a.Text)
	a.TargetLang = strings.TrimSpace(a.TargetLang)
	a.SourceLang = strings.TrimSpace(a.SourceLang)
	a.Formality = strings.ToLower(strings.TrimSpace(a.Formality))
	if a.Formality == "" {
		a.Formality = "default"
	}

	if a.Text == "" {
		return failResult(start, "text is required"), nil
	}
	if a.TargetLang == "" {
		return failResult(start, "target_lang is required"), nil
	}
	if len([]byte(a.Text)) > translateMaxInputBytes {
		return failResult(start, fmt.Sprintf("text exceeds %d byte limit", translateMaxInputBytes)), nil
	}
	switch a.Formality {
	case "default", "informal", "formal":
	default:
		return failResult(start, "formality must be default, informal, or formal"), nil
	}

	translated, err := translateWithShim(ctx, a)
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	if translated == "" {
		translated, err = translateWithProvider(ctx, call, a)
		if err != nil {
			return failResult(start, err.Error()), nil
		}
	}
	translated = strings.TrimSpace(translated)
	if translated == "" {
		return failResult(start, "translation produced empty output"), nil
	}

	out := translateOutput{
		Translated: translated,
		SourceLang: a.SourceLang,
		TargetLang: a.TargetLang,
	}
	structured, err := json.Marshal(out)
	if err != nil {
		return failResult(start, "marshal output: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     translated,
		Structured: structured,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func translateWithShim(ctx context.Context, a translateArgs) (string, error) {
	raw := strings.TrimSpace(os.Getenv("V100_TRANSLATE_CMD"))
	if raw == "" {
		return "", nil
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", raw+` "$1" "$2" "$3"`, "sh", a.TargetLang, a.SourceLang, a.Formality)
	cmd.Stdin = strings.NewReader(a.Text)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("translate shim: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func translateWithProvider(ctx context.Context, call ToolCallContext, a translateArgs) (string, error) {
	if call.Provider == nil {
		return "", fmt.Errorf("no provider available for translation and V100_TRANSLATE_CMD is not set")
	}

	source := a.SourceLang
	if source == "" {
		source = "auto-detect"
	}
	formality := a.Formality
	if formality == "" {
		formality = "default"
	}

	resp, err := call.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:  call.RunID,
		StepID: call.StepID,
		Messages: []providers.Message{
			{
				Role:    "system",
				Content: "You are a precise translation engine. Translate the user's text according to the requested source language, target language, and formality. Output only the translated text with no preamble, no quotes, no markdown, and no explanations.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Source language: %s\nTarget language: %s\nFormality: %s\n\nText:\n%s", source, a.TargetLang, formality, a.Text),
			},
		},
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0),
		},
	})
	if err != nil {
		return "", fmt.Errorf("translation failed: %w", err)
	}
	return strings.TrimSpace(resp.AssistantText), nil
}
