package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

type stubTranslateProvider struct {
	lastReq providers.CompleteRequest
	text    string
}

func (p *stubTranslateProvider) Name() string { return "stub-translate" }
func (p *stubTranslateProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}
func (p *stubTranslateProvider) Complete(_ context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	p.lastReq = req
	return providers.CompleteResponse{AssistantText: p.text}, nil
}
func (p *stubTranslateProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (p *stubTranslateProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

func TestTranslateSchemasAreValid(t *testing.T) {
	tool := Translate()
	for name, raw := range map[string]json.RawMessage{
		"input":  tool.InputSchema(),
		"output": tool.OutputSchema(),
	} {
		if !json.Valid(raw) {
			t.Fatalf("%s schema is invalid JSON: %s", name, raw)
		}
	}
	if !strings.Contains(string(tool.InputSchema()), `"required": ["text", "target_lang"]`) {
		t.Fatalf("input schema does not require text and target_lang: %s", tool.InputSchema())
	}
}

func TestTranslateRejectsInvalidArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "missing text", args: `{"target_lang":"fr"}`, want: "text is required"},
		{name: "missing target", args: `{"text":"hello"}`, want: "target_lang is required"},
		{name: "oversized", args: `{"text":"` + strings.Repeat("x", translateMaxInputBytes+1) + `","target_lang":"fr"}`, want: "text exceeds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Translate().Exec(context.Background(), ToolCallContext{}, json.RawMessage(tt.args))
			if err != nil {
				t.Fatalf("Exec returned error: %v", err)
			}
			if res.OK {
				t.Fatalf("result OK, want failure: %#v", res)
			}
			if !strings.Contains(res.Output, tt.want) {
				t.Fatalf("output = %q, want containing %q", res.Output, tt.want)
			}
		})
	}
}

func TestTranslateUsesConfiguredShim(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "translate-shim")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
input=$(cat)
printf 'target=%s source=%s formality=%s text=%s' "$1" "$2" "$3" "$input"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("V100_TRANSLATE_CMD", script)

	res, err := Translate().Exec(context.Background(), ToolCallContext{}, json.RawMessage(`{
		"text":"bonjour",
		"source_lang":"fr",
		"target_lang":"en",
		"formality":"formal"
	}`))
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if !res.OK {
		t.Fatalf("translate failed: %s", res.Output)
	}
	var structured map[string]string
	if err := json.Unmarshal(res.Structured, &structured); err != nil {
		t.Fatalf("structured unmarshal: %v", err)
	}
	want := "target=en source=fr formality=formal text=bonjour"
	if structured["translated"] != want || res.Output != want {
		t.Fatalf("translation = output %q structured %#v, want %q", res.Output, structured, want)
	}
	if structured["target_lang"] != "en" || structured["source_lang"] != "fr" {
		t.Fatalf("language echo = %#v", structured)
	}
}

func TestTranslateUsesProviderWhenNoShimConfigured(t *testing.T) {
	t.Setenv("V100_TRANSLATE_CMD", "")
	prov := &stubTranslateProvider{text: "Bonjour le monde"}

	res, err := Translate().Exec(context.Background(), ToolCallContext{Provider: prov}, json.RawMessage(`{
		"text":"Hello world",
		"target_lang":"fr"
	}`))
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
	if !res.OK {
		t.Fatalf("translate failed: %s", res.Output)
	}
	var structured map[string]string
	if err := json.Unmarshal(res.Structured, &structured); err != nil {
		t.Fatalf("structured unmarshal: %v", err)
	}
	if structured["translated"] != "Bonjour le monde" || structured["target_lang"] != "fr" {
		t.Fatalf("structured = %#v", structured)
	}
	if len(prov.lastReq.Messages) != 2 {
		t.Fatalf("provider messages = %#v", prov.lastReq.Messages)
	}
	if prov.lastReq.GenParams.Temperature == nil || *prov.lastReq.GenParams.Temperature != 0 {
		t.Fatalf("temperature = %#v, want 0", prov.lastReq.GenParams.Temperature)
	}
	if !strings.Contains(prov.lastReq.Messages[1].Content, "Hello world") {
		t.Fatalf("provider prompt missing source text: %#v", prov.lastReq.Messages)
	}
}

func TestTranslateToolMetadata(t *testing.T) {
	tool := Translate()
	if tool.Name() != "translate" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if tool.DangerLevel() != Safe {
		t.Fatalf("DangerLevel() = %q, want safe", tool.DangerLevel())
	}
	if eff := tool.Effects(); eff != (ToolEffects{}) {
		t.Fatalf("Effects() = %+v, want no direct effects", eff)
	}
}
