package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	case "/auto", "/local", "/codex", "/gemini", "/minimax", "/glm", "/anthropic", "/claude", "/openai", "/ollama", "/llamacpp", "/model":
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
	case "/codex", "/gemini", "/minimax", "/glm", "/anthropic", "/claude", "/openai", "/ollama", "/llamacpp":
		return buildSingleProviderSelection(cfg, strings.TrimPrefix(mode, "/"), strings.TrimPrefix(mode, "/"))
	case "/model":
		return sessionSelection{}, nil // handled separately in applyInteractiveMode
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

	if mode == "/model" {
		return handleModelCommand(ctx, cfg, loop, rest)
	}

	selection, err := buildSessionSelection(cfg, mode)
	if err != nil {
		return "", true, err
	}

	// Handle model list request (/provider model or /provider models)
	if rest == "model" || rest == "models" {
		printModelList(loop, cfg, strings.TrimPrefix(mode, "/"))
		return "", true, nil
	}

	// Handle model selection with fuzzy matching
	if strings.TrimSpace(rest) != "" {
		matchedModel := fuzzyMatchModel(selection.Provider, rest)
		if matchedModel != "" {
			selection.Model = matchedModel
		}
	}

	loop.Provider = selection.Provider
	loop.Model = selection.Model
	loop.Solver = selection.Solver
	loop.CompressProvider = selection.CompressProvider
	if meta, err := selection.Provider.Metadata(ctx, selection.Model); err == nil {
		loop.ModelMetadata = meta
	}
	emitSessionNotice(loop, fmt.Sprintf("session mode switched to %s (model: %s)", selection.Label, selection.Model))
	if strings.TrimSpace(rest) == "" || rest == "model" || rest == "models" {
		return "", true, nil
	}
	return rest, false, nil
}

func handleModelCommand(ctx context.Context, cfg *config.Config, loop *core.Loop, rest string) (string, bool, error) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		providers := getSortedProviders(cfg)
		var sb strings.Builder
		sb.WriteString("Available providers:\n")
		for i, p := range providers {
			fmt.Fprintf(&sb, " %d. %s\n", i+1, p)
		}
		sb.WriteString("\nType `/model <name_or_number>` to list models for a provider.")
		emitSessionNotice(loop, sb.String())
		return "", true, nil
	}

	providerQuery := fields[0]
	providerName := resolveProviderQuery(cfg, providerQuery)
	if providerName == "" {
		emitSessionNotice(loop, fmt.Sprintf("Unknown provider: %s", providerQuery))
		return "", true, nil
	}

	if len(fields) == 1 {
		printModelList(loop, cfg, providerName)
		emitSessionNotice(loop, fmt.Sprintf("\nType `/model %s <name_or_number>` to switch.", providerName))
		return "", true, nil
	}

	modelQuery := fields[1]
	task := strings.TrimSpace(strings.Join(fields[2:], " "))

	prov, err := buildProvider(cfg, providerName)
	if err != nil {
		return "", true, err
	}

	modelName := modelQuery
	if lister, ok := prov.(providers.ModelLister); ok {
		models := lister.Models()
		if idx, err := strconv.Atoi(modelQuery); err == nil {
			if idx > 0 && idx <= len(models) {
				modelName = models[idx-1].Name
			}
		} else if matched := fuzzyMatchModel(prov, modelQuery); matched != "" {
			modelName = matched
		}
	}

	loop.Provider = prov
	loop.Model = modelName
	loop.Solver = &core.ReactSolver{} // default for /model switch
	if meta, err := prov.Metadata(ctx, modelName); err == nil {
		loop.ModelMetadata = meta
	}
	emitSessionNotice(loop, fmt.Sprintf("session mode switched to %s (model: %s)", providerName, modelName))

	if task == "" {
		return "", true, nil
	}
	return task, false, nil
}

func getSortedProviders(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		if name == "smartrouter" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveProviderQuery(cfg *config.Config, query string) string {
	providers := getSortedProviders(cfg)
	if idx, err := strconv.Atoi(query); err == nil {
		if idx > 0 && idx <= len(providers) {
			return providers[idx-1]
		}
	}
	query = strings.ToLower(query)
	for _, p := range providers {
		if p == query {
			return p
		}
	}
	return ""
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

// fuzzyMatchModel returns the best model name match for a query, or "" if no match.
func fuzzyMatchModel(prov providers.Provider, query string) string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ""
	}

	lister, ok := prov.(providers.ModelLister)
	if !ok {
		return ""
	}

	models := lister.Models()
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.Name), query) {
			return m.Name
		}
	}
	return ""
}

// printModelList emits available models for a provider to the session.
func printModelList(loop *core.Loop, cfg *config.Config, providerName string) {
	prov, err := buildProvider(cfg, providerName)
	if err != nil {
		emitSessionNotice(loop, fmt.Sprintf("error building provider %q: %v", providerName, err))
		return
	}

	lister, ok := prov.(providers.ModelLister)
	if !ok {
		emitSessionNotice(loop, fmt.Sprintf("no model list available for %s", providerName))
		return
	}

	models := lister.Models()
	if len(models) == 0 {
		emitSessionNotice(loop, fmt.Sprintf("no models available for %s", providerName))
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s available models:\n", providerName)
	for i, m := range models {
		fmt.Fprintf(&sb, " %d. %s — %s\n", i+1, m.Name, m.Description)
	}
	emitSessionNotice(loop, sb.String())
}
