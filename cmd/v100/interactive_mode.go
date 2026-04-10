package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

type sessionSelection struct {
	Label            string
	ProviderName     string
	Model            string
	Provider         providers.Provider
	Solver           core.Solver
	CompressProvider providers.Provider
}

func parseInteractiveModeCommand(input string) (mode string, rest string, ok bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return "", input, false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", input, false
	}
	mode = strings.ToLower(fields[0])
	switch mode {
	case "/auto", "/local", "/codex", "/gemini", "/minimax":
	default:
		return "", input, false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	return mode, rest, true
}

func buildSessionSelection(cfg *config.Config, mode string) (sessionSelection, error) {
	switch mode {
	case "/auto":
		prov, err := buildProvider(cfg, "smartrouter")
		if err != nil {
			return sessionSelection{}, err
		}
		solver, err := buildSolver(cfg, "smartrouter")
		if err != nil {
			return sessionSelection{}, err
		}
		model := ""
		if smartProvName := resolveSmartProviderName(cfg); smartProvName != "" {
			if pc, ok := cfg.Providers[smartProvName]; ok {
				model = pc.DefaultModel
			}
		}
		return sessionSelection{
			Label:            "auto",
			ProviderName:     prov.Name(),
			Model:            model,
			Provider:         prov,
			Solver:           solver,
			CompressProvider: buildCompressProvider(cfg),
		}, nil
	case "/local":
		localName := resolveLocalProviderName(cfg)
		if localName == "" {
			return sessionSelection{}, fmt.Errorf("no local provider configured for /local")
		}
		return buildSingleProviderSelection(cfg, localName, "local")
	case "/codex", "/gemini", "/minimax":
		return buildSingleProviderSelection(cfg, strings.TrimPrefix(mode, "/"), strings.TrimPrefix(mode, "/"))
	default:
		return sessionSelection{}, fmt.Errorf("unsupported session mode %q", mode)
	}
}

func buildSingleProviderSelection(cfg *config.Config, providerName, label string) (sessionSelection, error) {
	prov, err := buildProvider(cfg, providerName)
	if err != nil {
		return sessionSelection{}, err
	}
	model := ""
	if pc, ok := cfg.Providers[providerName]; ok {
		model = pc.DefaultModel
	}
	return sessionSelection{
		Label:            label,
		ProviderName:     providerName,
		Model:            model,
		Provider:         prov,
		Solver:           &core.ReactSolver{},
		CompressProvider: buildCompressProvider(cfg),
	}, nil
}

func applyInteractiveMode(ctx context.Context, cfg *config.Config, loop *core.Loop, input string, planMode bool) (string, bool, error) {
	if planMode {
		return input, false, nil
	}
	mode, rest, ok := parseInteractiveModeCommand(input)
	if !ok {
		return input, false, nil
	}
	selection, err := buildSessionSelection(cfg, mode)
	if err != nil {
		return "", true, err
	}
	loop.Provider = selection.Provider
	loop.Solver = selection.Solver
	loop.CompressProvider = selection.CompressProvider
	if meta, err := selection.Provider.Metadata(ctx, selection.Model); err == nil {
		loop.ModelMetadata = meta
	}
	emitSessionNotice(loop, fmt.Sprintf("session mode switched to %s", selection.Label))
	if strings.TrimSpace(rest) == "" {
		return "", true, nil
	}
	return rest, false, nil
}

func emitSessionNotice(loop *core.Loop, message string) {
	if loop == nil || strings.TrimSpace(message) == "" {
		return
	}
	payload := core.UserMsgPayload{
		Content: message,
		Source:  "system",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := core.Event{
		TS:      time.Now().UTC(),
		RunID:   loop.Run.ID,
		EventID: fmt.Sprintf("ev-%d", time.Now().UTC().UnixNano()),
		Type:    core.EventUserMsg,
		Payload: data,
	}
	if loop.Trace != nil {
		_ = loop.Trace.Write(ev)
	}
	if loop.OutputFn != nil {
		loop.OutputFn(ev)
	}
}
