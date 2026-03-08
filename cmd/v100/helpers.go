package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
	"github.com/tripledoublev/v100/internal/ui"
)

func loadConfig(cfgPath string) (*config.Config, error) {
	if cfgPath == "" {
		cfgPath = config.XDGConfigPath()
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return config.DefaultConfig(), nil
	}
	return config.Load(cfgPath)
}

func buildProvider(cfg *config.Config, providerName string) (providers.Provider, error) {
	pc, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	return buildProviderFromConfig(pc)
}

func buildProviderFromConfig(pc config.ProviderConfig) (providers.Provider, error) {
	if pc.Type == "codex" {
		pc.DefaultModel, _ = normalizeCodexModelOverride(pc.DefaultModel)
	}
	switch pc.Type {
	case "codex":
		return providers.NewCodexProvider("", pc.DefaultModel)
	case "openai":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "OPENAI_API_KEY"
		}
		return providers.NewOpenAIProvider(authEnv, pc.BaseURL, pc.DefaultModel)
	case "ollama":
		return providers.NewOllamaProvider(pc.BaseURL, pc.DefaultModel)
	case "gemini":
		return providers.NewGeminiProvider("", pc.DefaultModel)
	case "anthropic":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "ANTHROPIC_API_KEY"
		}
		return providers.NewAnthropicProvider(authEnv, pc.DefaultModel)
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
}

func buildToolRegistry(cfg *config.Config) *tools.Registry {
	reg := tools.NewRegistry(cfg.Tools.Enabled)
	reg.Register(tools.FSRead())
	reg.Register(tools.FSWrite())
	reg.Register(tools.FSList())
	reg.Register(tools.FSMkdir())
	reg.Register(tools.BlackboardRead())
	reg.Register(tools.BlackboardWrite())
	reg.Register(tools.BlackboardSearch())
	reg.Register(tools.BlackboardStore())
	reg.Register(tools.Sh())
	reg.Register(tools.GitStatus())
	reg.Register(tools.GitDiff())
	reg.Register(tools.GitCommit())
	reg.Register(tools.GitPush())
	reg.Register(tools.CurlFetch())
	reg.Register(tools.PatchApply())
	reg.Register(tools.ProjectSearch())
	reg.Register(tools.SemDiff())
	reg.Register(tools.SemImpact())
	reg.Register(tools.SemBlame())
	reg.Register(tools.FSOutline())
	return reg
}

func buildSandboxSession(cfg *config.Config, runID, sourceWorkspace, runBase string) (executor.Session, *core.PathMapper, string, error) {
	execFactory, err := executor.NewExecutor(cfg.Sandbox, runBase)
	if err != nil {
		return nil, nil, "", err
	}
	session, err := execFactory.NewSession(runID, sourceWorkspace)
	if err != nil {
		return nil, nil, "", err
	}

	sandboxWorkspace := sourceWorkspace
	if cfg.Sandbox.Enabled {
		if err := session.Start(context.Background()); err != nil {
			return nil, nil, "", err
		}
		sandboxWorkspace = session.Workspace()
	}

	mapper := core.NewPathMapper(sourceWorkspace, sandboxWorkspace)
	return session, mapper, sandboxWorkspace, nil
}

func registerAgentTool(cfg *config.Config, reg *tools.Registry, trace *core.TraceWriter,
	budget *core.BudgetTracker, outputFn *core.OutputFn, confirmFn core.ConfirmFn, workspace string, parentMaxToolCalls int, session executor.Session, mapper *core.PathMapper) {

	providerBuilder := func(model string) (providers.Provider, string, error) {
		pc, ok := cfg.Providers[cfg.Defaults.Provider]
		if !ok {
			return nil, "", fmt.Errorf("provider %q not configured", cfg.Defaults.Provider)
		}
		if model != "" {
			normalized, changed := normalizeModelOverride(pc.Type, model)
			if changed {
				pc.DefaultModel = normalized
			} else {
				pc.DefaultModel = model
			}
		}
		prov, err := buildProviderFromConfig(pc)
		return prov, pc.DefaultModel, err
	}

	runFn := func(ctx context.Context, params tools.AgentRunParams) tools.AgentRunResult {
		var roleCfg config.AgentConfig
		if strings.TrimSpace(params.Agent) != "" {
			cfgRole, ok := cfg.Agents[params.Agent]
			if !ok {
				return tools.AgentRunResult{OK: false, Result: "unknown agent role: " + params.Agent}
			}
			roleCfg = cfgRole
		}

		modelOverride := strings.TrimSpace(params.Model)
		if modelOverride == "" {
			modelOverride = strings.TrimSpace(roleCfg.Model)
		}

		// Build provider
		prov, effectiveModel, err := providerBuilder(modelOverride)
		if err != nil {
			return tools.AgentRunResult{OK: false, Result: "build provider: " + err.Error()}
		}

		// Build child tool registry.
		parentTools := reg.EnabledTools()
		wantTools := make(map[string]bool)
		switch {
		case len(params.Tools) > 0:
			for _, tn := range params.Tools {
				if tn != "agent" && tn != "dispatch" {
					wantTools[tn] = true
				}
			}
		case len(roleCfg.Tools) > 0:
			for _, tn := range roleCfg.Tools {
				if tn != "agent" && tn != "dispatch" {
					wantTools[tn] = true
				}
			}
		default:
			for _, pt := range parentTools {
				if pt.Name() != "agent" && pt.Name() != "dispatch" {
					wantTools[pt.Name()] = true
				}
			}
		}

		enabledNames := make([]string, 0, len(wantTools))
		for n := range wantTools {
			enabledNames = append(enabledNames, n)
		}
		childReg := tools.NewRegistry(enabledNames)
		for _, pt := range parentTools {
			if wantTools[pt.Name()] {
				childReg.Register(pt)
			}
		}

		// Cap child budget by parent's remaining budget
		maxSteps := params.MaxSteps
		if maxSteps <= 0 {
			maxSteps = roleCfg.BudgetSteps
		}
		if maxSteps <= 0 {
			maxSteps = 10
		}
		if rem := budget.RemainingSteps(); rem > 0 && maxSteps > rem {
			maxSteps = rem
		}
		// Token cap: inherit parent remaining budget first; fall back to configured default.
		// 0 means unlimited in BudgetTracker semantics.
		maxTokens := 0
		if roleCfg.BudgetTokens > 0 {
			maxTokens = roleCfg.BudgetTokens
		}
		if maxTokens <= 0 && cfg.Defaults.BudgetTokens > 0 {
			maxTokens = cfg.Defaults.BudgetTokens
		}
		if rem := budget.RemainingTokens(); rem > 0 && (maxTokens == 0 || maxTokens > rem) {
			maxTokens = rem
		}
		maxCost := 0.0
		if roleCfg.BudgetCostUSD > 0 {
			maxCost = roleCfg.BudgetCostUSD
		}
		if rem := budget.RemainingCost(); rem > 0 && (maxCost == 0 || maxCost > rem) {
			maxCost = rem
		}

		childBudget := core.NewBudgetTracker(&core.Budget{
			MaxSteps:   maxSteps,
			MaxTokens:  maxTokens,
			MaxCostUSD: maxCost,
		})

		callIDForRun := strings.TrimSpace(params.CallID)
		if callIDForRun == "" {
			callIDForRun = fmt.Sprintf("anon-%x", randBytes(4))
		}
		childRunID := fmt.Sprintf("agent-%s", callIDForRun)
		childRun := &core.Run{
			ID:  childRunID,
			Dir: workspace,
		}

		systemPrompt := strings.TrimSpace(roleCfg.SystemPrompt)
		if systemPrompt == "" {
			systemPrompt = "You are a focused sub-agent. Complete the given task concisely. Use the tools available to you."
		}
		policyName := "sub-agent"
		if strings.TrimSpace(params.Agent) != "" {
			policyName = "sub-agent:" + params.Agent
		}
		childPolicy := &policy.Policy{
			Name:         policyName,
			SystemPrompt: systemPrompt,
		}
		childPolicy.MaxToolCallsPerStep = parentMaxToolCalls
		if childPolicy.MaxToolCallsPerStep <= 0 {
			childPolicy.MaxToolCallsPerStep = cfg.Defaults.MaxToolCallsPerStep
		}
		if childPolicy.MaxToolCallsPerStep <= 0 {
			childPolicy.MaxToolCallsPerStep = 50
		}

		// Resolve output function and count tool uses
		var toolUseCount int
		var childOutputFn core.OutputFn
		if outputFn != nil {
			parentFn := *outputFn
			childOutputFn = func(ev core.Event) {
				if ev.Type == core.EventToolCall {
					toolUseCount++
				}
				parentFn(ev)
			}
		}

		// Emit agent.start event
		modelName := strings.TrimSpace(effectiveModel)
		if modelName == "" {
			modelName = prov.Name()
		}
		startPayload := core.AgentStartPayload{
			Agent:        params.Agent,
			ParentCallID: params.CallID,
			AgentRunID:   childRunID,
			Task:         params.Task,
			Model:        modelName,
			Tools:        childReg.List(),
			MaxSteps:     maxSteps,
		}
		emitAgentEvent(trace, childOutputFn, params.RunID, params.StepID,
			params.CallID+"-astart", core.EventAgentStart, startPayload)
		emitAgentEvent(trace, childOutputFn, params.RunID, params.StepID,
			params.CallID+"-adispatch", core.EventAgentDispatch, core.AgentDispatchPayload{
				Agent:        params.Agent,
				Pattern:      params.Pattern,
				ParentCallID: params.CallID,
				AgentRunID:   childRunID,
				Task:         params.Task,
			})

		childLoop := &core.Loop{
			Run:       childRun,
			Provider:  prov,
			Tools:     childReg,
			Policy:    childPolicy,
			Trace:     trace,
			Budget:    childBudget,
			ConfirmFn: confirmFn,
			OutputFn:  childOutputFn,
			Session:   session,
			Mapper:    mapper,
		}

		var result string
		var lastErr error
		ok := true
		taskPrompt := buildSubAgentTask(params.Agent, params.Task, "", 1)
		if stepErr := childLoop.Step(ctx, taskPrompt); stepErr != nil {
			lastErr = stepErr
		}
		result = extractLastAssistantText(childLoop.Messages)

		if !isCompliantAgentHandoff(params.Agent, result) && childBudget.RemainingSteps() != 0 {
			retryPrompt := buildSubAgentTask(params.Agent, params.Task, result, 2)
			if stepErr := childLoop.Step(ctx, retryPrompt); stepErr != nil {
				lastErr = stepErr
			}
			result = extractLastAssistantText(childLoop.Messages)
		}

		if !isCompliantAgentHandoff(params.Agent, result) {
			ok = false
			if lastErr != nil {
				result = fmt.Sprintf("sub-agent failed to produce a compliant handoff after 2 attempts: %v", lastErr)
			} else {
				preview := strings.TrimSpace(result)
				if len(preview) > 240 {
					preview = preview[:240] + "…"
				}
				if preview == "" {
					preview = "(empty)"
				}
				result = "sub-agent failed to produce a compliant handoff after 2 attempts; partial output: " + preview
			}
		}

		// Add child's consumed budget to parent
		cb := childBudget.Budget()
		_ = budget.AddTokens(cb.UsedTokens, 0)
		_ = budget.AddCost(cb.UsedCostUSD)

		// Emit agent.end event
		endPayload := core.AgentEndPayload{
			Agent:        params.Agent,
			ParentCallID: params.CallID,
			AgentRunID:   childRunID,
			OK:           ok,
			Result:       result,
			ToolUses:     toolUseCount,
			UsedSteps:    cb.UsedSteps,
			UsedTokens:   cb.UsedTokens,
			CostUSD:      cb.UsedCostUSD,
		}
		emitAgentEvent(trace, childOutputFn, params.RunID, params.StepID,
			params.CallID+"-aend", core.EventAgentEnd, endPayload)

		return tools.AgentRunResult{
			OK:         ok,
			Result:     result,
			UsedSteps:  cb.UsedSteps,
			UsedTokens: cb.UsedTokens,
			CostUSD:    cb.UsedCostUSD,
		}
	}

	reg.Register(tools.NewAgent(runFn))
	reg.Register(tools.NewDispatch(runFn, func() []string {
		names := make([]string, 0, len(cfg.Agents))
		for k := range cfg.Agents {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}))
	reg.Register(tools.NewOrchestrate(runFn, func() []string {
		names := make([]string, 0, len(cfg.Agents))
		for k := range cfg.Agents {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}))
}

func buildSubAgentTask(agent, task, priorOutput string, attempt int) string {
	base := strings.TrimSpace(task)
	if base == "" {
		base = "(no task provided)"
	}
	contract := `
Return a final handoff with this exact structure:
## Summary
<2-4 sentences>

## Findings
- [P1|P2|P3] <issue> — <why it matters> — <file refs if available>
- [P1|P2|P3] ...

## Next Steps
1. <first action>
2. <second action>

Rules:
- Never return an empty response.
- If tools fail, still return the handoff and explain what failed.
- Keep total length under 350 words.
`
	if strings.EqualFold(strings.TrimSpace(agent), "researcher") {
		contract = `
Return a final handoff with this exact structure:
## Summary
<2-4 sentences>

## Key Files
- <path> — <why this file matters for the task>
- <path> — <why this file matters for the task>

## Findings
- <finding with file reference and short evidence>
- <finding with file reference and short evidence>

## Next Steps
1. <first action>
2. <second action>

## JSON
{"agent":"researcher","files":["path1","path2"],"findings":["..."],"confidence":"low|medium|high"}

Rules:
- Never return an empty response.
- If tools fail, still return the handoff and explain what failed.
- Keep total length under 350 words.
`
	}
	if attempt <= 1 {
		return base + "\n\n" + strings.TrimSpace(contract)
	}
	return base + "\n\nYour previous response was not compliant or empty.\nPrevious output:\n" +
		strings.TrimSpace(priorOutput) + "\n\n" + strings.TrimSpace(contract)
}

func extractLastAssistantText(msgs []providers.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			v := strings.TrimSpace(msgs[i].Content)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func isCompliantAgentHandoff(agent, s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if len(s) < 80 {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(agent), "researcher") {
		return strings.Contains(s, "## Summary") &&
			strings.Contains(s, "## Key Files") &&
			strings.Contains(s, "## Findings") &&
			strings.Contains(s, "## Next Steps")
	}
	return strings.Contains(s, "## Summary") &&
		strings.Contains(s, "## Findings") &&
		strings.Contains(s, "## Next Steps")
}

func emitAgentEvent(trace *core.TraceWriter, outputFn core.OutputFn,
	runID, stepID, eventID string, eventType core.EventType, payload any) {
	b, _ := json.Marshal(payload)
	ev := core.Event{
		TS:      time.Now().UTC(),
		RunID:   runID,
		StepID:  stepID,
		EventID: eventID,
		Type:    eventType,
		Payload: b,
	}
	if trace != nil {
		_ = trace.Write(ev)
	}
	if outputFn != nil {
		outputFn(ev)
	}
}

func loadPolicy(cfg *config.Config, name string) *policy.Policy {
	if name == "" {
		name = "default"
	}
	pc, ok := cfg.Policies[name]
	if !ok {
		return policy.Default()
	}
	p, err := policy.Load(name, pc)
	if err != nil {
		return policy.Default()
	}
	return p
}

func buildGenParams(cfg *config.Config, temperature, topP float64, topK, maxTokens, seed int, cmd *cobra.Command) providers.GenParams {
	gp := providers.GenParams{}
	// Apply config defaults first
	if cfg.Defaults.Temperature != nil {
		gp.Temperature = cfg.Defaults.Temperature
	}
	if cfg.Defaults.TopP != nil {
		gp.TopP = cfg.Defaults.TopP
	}
	if cfg.Defaults.TopK != nil {
		gp.TopK = cfg.Defaults.TopK
	}
	if cfg.Defaults.MaxTokens > 0 {
		gp.MaxTokens = cfg.Defaults.MaxTokens
	}
	if cfg.Defaults.Seed != nil {
		gp.Seed = cfg.Defaults.Seed
	}
	// Override with CLI flags (only if explicitly set)
	if cmd.Flags().Changed("temperature") {
		gp.Temperature = &temperature
	}
	if cmd.Flags().Changed("top-p") {
		gp.TopP = &topP
	}
	if cmd.Flags().Changed("top-k") {
		gp.TopK = &topK
	}
	if cmd.Flags().Changed("max-tokens") {
		gp.MaxTokens = maxTokens
	}
	if cmd.Flags().Changed("seed") {
		gp.Seed = &seed
	}
	return gp
}

func buildConfirmFn(mode string) core.ConfirmFn {
	switch mode {
	case "always":
		return ui.ConfirmTool
	case "never":
		return func(_, _ string) bool { return true }
	default: // "dangerous"
		return ui.ConfirmTool
	}
}

func reconstructHistory(events []core.Event) ([]providers.Message, string, string, string) {
	var msgs []providers.Message
	var providerName, model, workspace string

	for _, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			providerName = p.Provider
			model = p.Model
			workspace = strings.TrimSpace(p.Workspace)

		case core.EventUserMsg:
			var p core.UserMsgPayload
			_ = json.Unmarshal(ev.Payload, &p)
			msgs = append(msgs, providers.Message{Role: "user", Content: p.Content})

		case core.EventModelResp:
			var p core.ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			msgs = append(msgs, providers.Message{Role: "assistant", Content: p.Text})

		case core.EventToolResult:
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			content := p.Output
			if !p.OK {
				content = "ERROR: " + p.Output
			}
			msgs = append(msgs, providers.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: p.CallID,
				Name:       p.Name,
			})
		}
	}
	return msgs, providerName, model, workspace
}

func resolveWorkspace(workspaceFlag, runDir string) string {
	workspace := strings.TrimSpace(workspaceFlag)
	if workspace == "" {
		// Default to caller CWD so the agent operates on the project by default.
		if wd, err := os.Getwd(); err == nil && strings.TrimSpace(wd) != "" {
			workspace = wd
		} else {
			workspace = runDir
		}
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		return abs
	}
	return workspace
}

func findRunDir(runID string) (string, error) {
	// Try runs/<runID> first
	candidate := filepath.Join("runs", runID)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Try exact path
	if _, err := os.Stat(runID); err == nil {
		return runID, nil
	}
	return "", fmt.Errorf("run %q not found (checked runs/%s)", runID, runID)
}

func findInPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		full := filepath.Join(dir, name)
		if _, err := os.Stat(full); err == nil {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

func parseTags(raw []string) map[string]string {
	tags := make(map[string]string)
	for _, s := range raw {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			tags[parts[0]] = parts[1]
		}
	}
	return tags
}

func normalizeModelOverride(providerType, model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	if providerType != "codex" {
		return model, false
	}
	return normalizeCodexModelOverride(model)
}

func normalizeCodexModelOverride(model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	switch strings.ToLower(model) {
	case "gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano":
		return "gpt-5.3-codex", true
	default:
		return model, false
	}
}

func newRunID() string {
	// Simple time-based ID
	return fmt.Sprintf("%s-%x", time.Now().UTC().Format("20060102T150405"), randBytes(4))
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	// Use crypto/rand via os
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return b
	}
	defer f.Close()
	_, _ = f.Read(b)
	return b
}
