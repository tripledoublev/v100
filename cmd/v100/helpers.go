package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

func configuredAgentNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func formatUnknownAgentRole(cfg *config.Config, role string) string {
	role = strings.TrimSpace(role)
	names := configuredAgentNames(cfg)
	if len(names) == 0 {
		return fmt.Sprintf("unknown agent role: %s (no roles configured; add [agents.<name>] to config)", role)
	}
	return fmt.Sprintf("unknown agent role: %s (available: %s; run `v100 agents`)", role, strings.Join(names, ", "))
}

func validateExecutionSafety(cfg *config.Config, confirmMode string, allowUnsafeHost bool) error {
	if cfg == nil {
		return nil
	}
	if cfg.Sandbox.Enabled {
		return nil
	}
	if strings.TrimSpace(confirmMode) != "never" {
		return nil
	}
	if allowUnsafeHost {
		return nil
	}
	return fmt.Errorf("refusing to run with confirmations disabled on the host workspace; enable --sandbox or pass --unsafe to acknowledge the risk")
}

func buildProvider(cfg *config.Config, providerName string) (providers.Provider, error) {
	pc, ok := cfg.Providers[providerName]
	if !ok {
		// Fall back to built-in defaults (e.g. minimax after login)
		defaults := config.DefaultConfig()
		pc, ok = defaults.Providers[providerName]
		if !ok {
			return nil, fmt.Errorf("provider %q not configured", providerName)
		}
	}
	if pc.Type == "smartrouter" {
		return buildSmartRouterProvider(cfg, "")
	}
	return buildProviderFromConfig(pc)
}

func ensureProviderConfig(cfg *config.Config, providerName string) {
	if cfg == nil {
		return
	}
	if _, ok := cfg.Providers[providerName]; ok {
		return
	}
	defaults := config.DefaultConfig()
	if pc, ok := defaults.Providers[providerName]; ok {
		cfg.Providers[providerName] = pc
		return
	}
	cfg.Providers[providerName] = config.ProviderConfig{
		Type: providerName,
	}
}

func buildProviderWithModel(cfg *config.Config, providerName, model string) (providers.Provider, error) {
	ensureProviderConfig(cfg, providerName)
	pc, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", providerName)
	}
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		pc.DefaultModel = trimmed
	}
	if pc.Type == "smartrouter" {
		return buildSmartRouterProvider(cfg, strings.TrimSpace(model))
	}
	return buildProviderFromConfig(pc)
}

func providerConfigType(cfg *config.Config, providerName string) string {
	if cfg == nil {
		return ""
	}
	if pc, ok := cfg.Providers[providerName]; ok && strings.TrimSpace(pc.Type) != "" {
		return pc.Type
	}
	defaults := config.DefaultConfig()
	if pc, ok := defaults.Providers[providerName]; ok {
		return pc.Type
	}
	return ""
}

func isLocalProviderType(providerType string) bool {
	switch providerType {
	case "ollama", "llamacpp", "llama.cpp", "llama-cpp":
		return true
	default:
		return false
	}
}

func isLocalProviderName(cfg *config.Config, providerName string) bool {
	return isLocalProviderType(providerConfigType(cfg, providerName))
}

func resolveLocalProviderName(cfg *config.Config) string {
	candidates := []string{
		cfg.Defaults.CheapProvider,
		"ollama",
		"llamacpp",
	}
	for _, name := range candidates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if isLocalProviderName(cfg, name) {
			return name
		}
	}
	for _, name := range candidates {
		name = strings.TrimSpace(name)
		if name != "" {
			return name
		}
	}
	return ""
}

func resolveSmartProviderName(cfg *config.Config) string {
	candidate := strings.TrimSpace(cfg.Defaults.SmartProvider)
	if candidate != "" && candidate != "smartrouter" {
		return candidate
	}
	candidate = strings.TrimSpace(cfg.Defaults.Provider)
	if candidate != "" && candidate != "smartrouter" {
		return candidate
	}
	return "minimax"
}

func buildSmartRouterProvider(cfg *config.Config, smartModel string) (providers.Provider, error) {
	cheapProvName := resolveLocalProviderName(cfg)
	if cheapProvName == "" {
		cheapProvName = "ollama"
	}
	smartProvName := resolveSmartProviderName(cfg)
	if smartProvName == "smartrouter" {
		return nil, fmt.Errorf("smartrouter smart provider cannot point to smartrouter")
	}
	if cheapProvName == "smartrouter" {
		return nil, fmt.Errorf("smartrouter cheap provider cannot point to smartrouter")
	}
	cheap, err := buildProvider(cfg, cheapProvName)
	if err != nil {
		return nil, fmt.Errorf("build cheap provider %q: %w", cheapProvName, err)
	}
	smart, err := buildProviderWithModel(cfg, smartProvName, smartModel)
	if err != nil {
		return nil, fmt.Errorf("build smart provider %q: %w", smartProvName, err)
	}
	return &providers.SmartRouterProvider{Cheap: cheap, Smart: smart}, nil
}

func normalizedProviderConfig(pc config.ProviderConfig) config.ProviderConfig {
	if pc.Type == "codex" {
		original := pc.DefaultModel
		normalized, changed := normalizeCodexModelOverride(pc.DefaultModel)
		// Fix #11: Log model normalization to prevent silent changes
		if changed {
			fmt.Fprintf(os.Stderr, "→ model normalized: %s → %s (%s)\n", original, normalized, pc.Type)
			pc.DefaultModel = normalized
		}
	}
	return pc
}

func buildProviderFromConfig(pc config.ProviderConfig) (providers.Provider, error) {
	pc = normalizedProviderConfig(pc)
	var raw providers.Provider
	var err error
	switch pc.Type {
	case "smartrouter":
		return nil, fmt.Errorf("smartrouter provider requires full config context")
	case "codex":
		raw, err = providers.NewCodexProvider("", pc.DefaultModel)
	case "openai":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "OPENAI_API_KEY"
		}
		raw, err = providers.NewOpenAIProvider(authEnv, pc.BaseURL, pc.DefaultModel)
	case "ollama":
		raw, err = providers.NewOllamaProvider(pc.BaseURL, pc.DefaultModel, pc.Auth.Username, pc.Auth.Password)
	case "llamacpp", "llama.cpp", "llama-cpp":
		raw, err = providers.NewLlamaCppProvider(pc.BaseURL, pc.DefaultModel)
	case "gemini":
		raw, err = providers.NewGeminiProvider("", pc.DefaultModel)
	case "anthropic":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "ANTHROPIC_API_KEY"
		}
		raw, err = providers.NewAnthropicProvider(authEnv, pc.DefaultModel)
	case "minimax":
		raw, err = providers.NewMiniMaxProvider("", pc.DefaultModel)
	case "glm":
		authEnv := pc.Auth.Env
		if authEnv == "" {
			authEnv = "ZHIPU_API_KEY"
		}
		raw, err = providers.NewGLMProvider(authEnv, pc.DefaultModel)
	default:
		return nil, fmt.Errorf("unknown provider type %q", pc.Type)
	}
	if err != nil {
		return nil, err
	}
	return providers.WithRetry(raw, providers.DefaultRetryConfig()), nil
}

func persistModelMetadata(runDir string, metadata providers.ModelMetadata) {
	if metadata == (providers.ModelMetadata{}) {
		return
	}
	meta, err := core.ReadMeta(runDir)
	if err != nil {
		return
	}
	meta.ModelMetadata = metadata
	_ = core.WriteMeta(runDir, meta)
}

func persistRunSelection(runDir, providerName, model string, metadata providers.ModelMetadata, clearMetadata bool) {
	meta, err := core.ReadMeta(runDir)
	if err != nil {
		return
	}
	meta.Provider = strings.TrimSpace(providerName)
	meta.Model = strings.TrimSpace(model)
	if clearMetadata || metadata != (providers.ModelMetadata{}) {
		meta.ModelMetadata = metadata
	}
	_ = core.WriteMeta(runDir, meta)
}

func resolveProviderMetadata(ctx context.Context, prov providers.Provider, model string, fallback providers.ModelMetadata) providers.ModelMetadata {
	if fallback != (providers.ModelMetadata{}) {
		return fallback
	}
	if prov == nil {
		return fallback
	}
	metadata, err := prov.Metadata(ctx, model)
	if err != nil {
		return fallback
	}
	return metadata
}

func traceWorkspace(cfg *config.Config, workspace string) string {
	if cfg != nil && cfg.Sandbox.Enabled {
		return "/workspace"
	}
	return workspace
}

func appendTraceEvent(trace *core.TraceWriter, runID string, eventType core.EventType, payload any) error {
	if trace == nil {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("trace event marshal: %w", err)
	}
	return trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   runID,
		EventID: fmt.Sprintf("ev-%d", time.Now().UTC().UnixNano()),
		Type:    eventType,
		Payload: b,
	})
}

func buildSolver(cfg *config.Config, solverName string) (core.Solver, error) {
	if solverName == "" {
		solverName = cfg.Defaults.Solver
	}

	switch solverName {
	case "plan_execute":
		maxReplans := cfg.Defaults.MaxReplans
		if maxReplans <= 0 {
			maxReplans = 3
		}
		return &core.PlanExecuteSolver{MaxReplans: maxReplans}, nil
	case "router", "smartrouter":
		cheapProvName := resolveLocalProviderName(cfg)
		if cheapProvName == "" {
			cheapProvName = "ollama"
		}
		smartProvName := resolveSmartProviderName(cfg)
		cheap, err := buildProvider(cfg, cheapProvName)
		if err != nil {
			return nil, fmt.Errorf("build cheap provider %q: %w", cheapProvName, err)
		}
		smart, err := buildProvider(cfg, smartProvName)
		if err != nil {
			return nil, fmt.Errorf("build smart provider %q: %w", smartProvName, err)
		}
		return &core.RouterSolver{Cheap: cheap, Smart: smart}, nil
	case "react", "":
		if solverName == "" && strings.TrimSpace(cfg.Defaults.Provider) == "smartrouter" {
			return buildSolver(cfg, "smartrouter")
		}
		return &core.ReactSolver{}, nil
	default:
		if solverName == "plan" {
			return nil, fmt.Errorf("unknown solver %q; did you mean %q?", solverName, "plan_execute")
		}
		return nil, fmt.Errorf("unknown solver %q", solverName)
	}
}

func solverDisplayName(s core.Solver) string {
	switch s.(type) {
	case *core.PlanExecuteSolver:
		return "plan_execute"
	case *core.RouterSolver:
		return "smartrouter"
	default:
		return "react"
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
	reg.Register(tools.WebExtract())
	reg.Register(tools.NewsFetch())
	reg.Register(tools.PatchApply())
	reg.Register(tools.ProjectSearch())
	reg.Register(tools.NewAgent(nil))
	reg.Register(tools.NewDispatch(nil, nil))
	reg.Register(tools.NewOrchestrate(nil, nil))
	reg.Register(tools.SemDiff())
	reg.Register(tools.SemImpact())
	reg.Register(tools.SemBlame())
	reg.Register(tools.FSOutline())
	reg.Register(tools.InspectTool())
	reg.Register(tools.NewReflect())
	return reg
}

func enabledToolSummary(reg *tools.Registry) string {
	return enabledToolSummaryVerbose(reg, false)
}

func enabledToolSummaryVerbose(reg *tools.Registry, verbose bool) string {
	if reg == nil {
		return "0 enabled"
	}
	names := reg.List()
	missing := reg.MissingEnabledNames()
	dangerous := 0
	for _, name := range names {
		if reg.IsDangerous(name) {
			dangerous++
		}
	}
	if len(names) == 0 {
		if len(missing) > 0 {
			return fmt.Sprintf("0 enabled (%d invalid: %s)", len(missing), strings.Join(missing, ", "))
		}
		return "0 enabled"
	}
	base := ""
	if verbose {
		base = fmt.Sprintf("%d enabled (%d dangerous): %s", len(names), dangerous, strings.Join(names, ", "))
	} else {
		base = fmt.Sprintf("%d enabled (%d dangerous)", len(names), dangerous)
	}
	if len(missing) > 0 {
		base += fmt.Sprintf(" [invalid enabled entries: %s]", strings.Join(missing, ", "))
	}
	return base
}

func validateToolRegistry(reg *tools.Registry) error {
	if reg == nil {
		return nil
	}
	if err := reg.Validate(); err != nil {
		return fmt.Errorf("tool registry: %w", err)
	}
	return nil
}

func buildSandboxSession(cfg *config.Config, runID, sourceWorkspace, runBase string) (executor.Session, *core.PathMapper, string, error) {
	sourceAbs, err := filepath.Abs(sourceWorkspace)
	if err != nil {
		return nil, nil, "", err
	}
	execFactory, err := executor.NewExecutor(cfg.Sandbox, runBase)
	if err != nil {
		return nil, nil, "", err
	}
	session, err := execFactory.NewSession(runID, sourceAbs)
	if err != nil {
		return nil, nil, "", err
	}

	sandboxWorkspace := sourceAbs
	if cfg.Sandbox.Enabled {
		if err := session.Start(context.Background()); err != nil {
			return nil, nil, "", err
		}
		sandboxWorkspace, err = filepath.Abs(session.Workspace())
		if err != nil {
			return nil, nil, "", err
		}
	}

	mapper := core.NewPathMapper(sourceAbs, sandboxWorkspace)
	return session, mapper, sandboxWorkspace, nil
}

func loopNetworkTier(cfg *config.Config) string {
	if cfg == nil || !cfg.Sandbox.Enabled {
		return "open"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Sandbox.NetworkTier)) {
	case "", "off":
		return "off"
	case "research":
		return "research"
	case "open":
		return "open"
	default:
		return "off"
	}
}

func buildSnapshotManager(cfg *config.Config, workspace string) core.SnapshotManager {
	if cfg == nil || !cfg.Sandbox.Enabled {
		return nil
	}
	return core.NewWorkspaceSnapshotManager(workspace, filepath.Join(filepath.Dir(workspace), "snapshots"))
}

func finalizeSandboxRun(cfg *config.Config, run *core.Run, reason string, mapper *core.PathMapper) (*core.SandboxFinalizeResult, error) {
	if cfg == nil || !cfg.Sandbox.Enabled || run == nil || mapper == nil {
		return nil, nil
	}
	if strings.TrimSpace(mapper.HostRoot) == "" || strings.TrimSpace(mapper.SandboxRoot) == "" {
		return nil, nil
	}
	if filepath.Clean(mapper.HostRoot) == filepath.Clean(mapper.SandboxRoot) {
		return nil, nil
	}

	meta, err := core.ReadMeta(filepath.Dir(run.TraceFile))
	if err != nil {
		meta = core.RunMeta{}
	}
	result, err := core.FinalizeSandboxWorkspace(core.SandboxFinalizeOptions{
		Mode:                cfg.Sandbox.ApplyBack,
		Success:             runReasonAllowsApplyBack(reason),
		SourceWorkspace:     mapper.HostRoot,
		SandboxWorkspace:    mapper.SandboxRoot,
		BaselineFingerprint: meta.SourceFingerprint,
		ArtifactDir:         filepath.Join(filepath.Dir(run.TraceFile), "artifacts"),
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func runReasonAllowsApplyBack(reason string) bool {
	switch reason {
	case "user_exit", "completed", "prompt_exit":
		return true
	default:
		return false
	}
}

func sandboxFinalizeMessage(result core.SandboxFinalizeResult) string {
	total := len(result.Diff.Added) + len(result.Diff.Modified) + len(result.Diff.Deleted)
	switch {
	case result.Applied:
		return fmt.Sprintf("sandbox apply-back complete: %d change(s) synced to source workspace", total)
	case result.Conflict:
		return fmt.Sprintf("sandbox apply-back blocked: source workspace changed; review %s", result.ArtifactPath)
	case result.Mode == "manual":
		return fmt.Sprintf("sandbox changes ready for review: %d change(s); see %s", total, result.ArtifactPath)
	case result.Mode == "never":
		return fmt.Sprintf("sandbox changes retained without apply-back: %d change(s); see %s", total, result.ArtifactPath)
	case result.SkippedReason == "run_not_successful":
		return fmt.Sprintf("sandbox apply-back skipped because the run did not end successfully; review %s", result.ArtifactPath)
	case result.SkippedReason == "missing_source_fingerprint":
		return fmt.Sprintf("sandbox apply-back skipped: missing source fingerprint; review %s", result.ArtifactPath)
	case result.SkippedReason == "no_changes":
		return "sandbox apply-back: no workspace changes"
	default:
		return fmt.Sprintf("sandbox apply-back skipped (%s); review %s", result.SkippedReason, result.ArtifactPath)
	}
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
				return tools.AgentRunResult{OK: false, Result: formatUnknownAgentRole(cfg, params.Agent)}
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
			Run:         childRun,
			Provider:    prov,
			Tools:       childReg,
			Policy:      childPolicy,
			Trace:       trace,
			Budget:      childBudget,
			ConfirmFn:   confirmFn,
			OutputFn:    childOutputFn,
			Session:     session,
			Mapper:      mapper,
			NetworkTier: loopNetworkTier(cfg),
			Snapshots:   buildSnapshotManager(cfg, workspace),
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
	var p *policy.Policy
	if !ok {
		p = policy.Default()
	} else {
		var err error
		p, err = policy.Load(name, pc)
		if err != nil {
			p = policy.Default()
		}
	}
	// Apply compression policy defaults from config
	if cfg.Defaults.MaxToolCallsPerStep > 0 {
		p.MaxToolCallsPerStep = cfg.Defaults.MaxToolCallsPerStep
	}
	if cfg.Defaults.MaxToolResultChars > 0 {
		p.MaxToolResultChars = cfg.Defaults.MaxToolResultChars
	}
	if cfg.Defaults.CompressProtectRecent > 0 {
		p.CompressProtectRecent = cfg.Defaults.CompressProtectRecent
	}
	if cfg.Defaults.MemoryMode != "" {
		p.MemoryMode = cfg.Defaults.MemoryMode
	}
	if cfg.Defaults.MemoryMaxTokens > 0 {
		p.MemoryMaxTokens = cfg.Defaults.MemoryMaxTokens
	}
	return p
}

func buildCompressProvider(cfg *config.Config) providers.Provider {
	cpName := cfg.Defaults.CompressProvider
	if cpName == "" {
		if local := resolveLocalProviderName(cfg); local != "" {
			cpName = local
		} else if cheap := strings.TrimSpace(cfg.Defaults.CheapProvider); cheap != "" {
			cpName = cheap
		} else {
			cpName = "gemini"
		}
	}
	if cpName == cfg.Defaults.Provider {
		return nil // same as main provider, no need to build separately
	}
	cp, err := buildProvider(cfg, cpName)
	if err != nil {
		return nil
	}
	return cp
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
		return func(toolName, _ string) bool {
			// git_push is an irreversible external side effect. Even in auto mode,
			// require an explicit higher-level path instead of silently approving it.
			return toolName != "git_push"
		}
	default: // "dangerous"
		return ui.ConfirmTool
	}
}

func reconstructHistory(runDir string, events []core.Event) ([]providers.Message, string, string, string, providers.ModelMetadata) {
	var msgs []providers.Message
	var providerName, model, workspace string
	var metadata providers.ModelMetadata

	for _, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var p core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			providerName = p.Provider
			model = p.Model
			workspace = p.Workspace
			metadata = p.ModelMetadata

		case core.EventUserMsg:
			var p core.UserMsgPayload
			_ = json.Unmarshal(ev.Payload, &p)
			msgs = append(msgs, providers.Message{Role: "user", Content: p.Content})

		case core.EventModelResp:
			var p core.ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			var toolCalls []providers.ToolCall
			for _, tc := range p.ToolCalls {
				toolCalls = append(toolCalls, providers.ToolCall{
					ID:   tc.ID,
					Name: tc.Name,
					Args: json.RawMessage(tc.ArgsJSON),
				})
			}
			msgs = append(msgs, providers.Message{Role: "assistant", Content: p.Text, ToolCalls: toolCalls})

		case core.EventToolResult:
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			content := sanitizeResumedToolOutput(p.Name, p.Output)
			if !p.OK {
				content = "ERROR: " + sanitizeResumedToolOutput(p.Name, p.Output)
			}
			msgs = append(msgs, providers.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: p.CallID,
				Name:       p.Name,
			})

		case core.EventSandboxRestore:
			var p core.SandboxRestorePayload
			_ = json.Unmarshal(ev.Payload, &p)
			if strings.TrimSpace(p.SnapshotID) == "" {
				continue
			}
			cp, err := core.ReadCheckpoint(runDir, p.SnapshotID)
			if err != nil {
				continue
			}
			msgs = make([]providers.Message, len(cp.Messages))
			copy(msgs, cp.Messages)
		}
	}
	return reconcileToolHistory(msgs), providerName, model, workspace, metadata
}

func resumeReplayEvents(events []core.Event) []core.Event {
	filtered := make([]core.Event, 0, len(events))
	for _, ev := range events {
		if ev.Type == core.EventRunEnd {
			continue
		}
		filtered = append(filtered, ev)
	}
	return filtered
}

func buildResumeSummary(runID string, events []core.Event, msgs []providers.Message) string {
	parts := make([]string, 0, 5)
	parts = append(parts, fmt.Sprintf("Resume summary for run %s.", runID))

	if reason, summary := latestRunEndDetails(events); reason != "" {
		line := fmt.Sprintf("Previous run ended with reason=%s.", reason)
		if strings.TrimSpace(summary) != "" {
			line += " Final summary: " + trimResumeSummaryLine(summary, 200)
		}
		parts = append(parts, line)
	}

	if goal := latestResumeMessageByRole(msgs, "user"); goal != "" {
		parts = append(parts, "Current user goal: "+trimResumeSummaryLine(goal, 220))
	}
	if state := latestResumeAssistantState(msgs); state != "" {
		parts = append(parts, "Last assistant state: "+trimResumeSummaryLine(state, 220))
	}
	if tools := recentResumeToolSummary(msgs, 4); tools != "" {
		parts = append(parts, "Recent successful tool results: "+tools+".")
	}

	parts = append(parts, "Continue from this state. Avoid re-reading broad repo context unless the prior summary is insufficient or the workspace has materially changed.")
	return strings.Join(parts, "\n")
}

func latestRunEndDetails(events []core.Event) (reason, summary string) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != core.EventRunEnd {
			continue
		}
		var p core.RunEndPayload
		_ = json.Unmarshal(events[i].Payload, &p)
		return strings.TrimSpace(p.Reason), strings.TrimSpace(p.Summary)
	}
	return "", ""
}

func latestResumeMessageByRole(msgs []providers.Message, role string) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != role {
			continue
		}
		if text := strings.TrimSpace(msgs[i].Content); text != "" {
			return text
		}
	}
	return ""
}

func latestResumeAssistantState(msgs []providers.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) > 0 {
			continue
		}
		if text := strings.TrimSpace(msg.Content); text != "" {
			return text
		}
	}
	return ""
}

func recentResumeToolSummary(msgs []providers.Message, limit int) string {
	if limit <= 0 {
		return ""
	}
	items := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for i := len(msgs) - 1; i >= 0 && len(items) < limit; i-- {
		msg := msgs[i]
		if msg.Role != "tool" || strings.TrimSpace(msg.Name) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(msg.Content), "ERROR:") {
			continue
		}
		label := msg.Name
		if summary := trimResumeSummaryLine(msg.Content, 72); summary != "" {
			label += " -> " + summary
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		items = append(items, label)
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return strings.Join(items, "; ")
}

func trimResumeSummaryLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return strings.TrimSpace(s[:max-1]) + "…"
}

func sanitizeResumedToolOutput(name, output string) string {
	if strings.TrimSpace(output) == "" {
		return output
	}
	if strings.EqualFold(strings.TrimSpace(name), "curl_fetch") {
		return sanitizeCurlFetchResumeOutput(output)
	}
	if looksBinaryLikeText(output) {
		return "[tool output omitted during resume: binary or non-text payload]"
	}
	return output
}

func sanitizeCurlFetchResumeOutput(output string) string {
	const marker = "\n\n"
	idx := strings.Index(output, marker)
	if idx == -1 {
		if looksBinaryLikeText(output) {
			return "[curl_fetch output omitted during resume: binary or non-text payload]"
		}
		return output
	}
	head := output[:idx]
	body := output[idx+len(marker):]
	if !looksBinaryLikeText(body) {
		return output
	}
	lines := strings.Split(head, "\n")
	contentType := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "content_type:") {
			contentType = strings.TrimSpace(line[len("content_type:"):])
			break
		}
	}
	summary := "[non-text response omitted during resume]"
	if contentType != "" {
		summary = fmt.Sprintf("[non-text response omitted during resume: %s]", contentType)
	}
	return head + marker + summary
}

func looksBinaryLikeText(s string) bool {
	if s == "" {
		return false
	}
	if !utf8.ValidString(s) {
		return true
	}
	control := 0
	repl := 0
	total := 0
	for _, r := range s {
		total++
		if r == '\uFFFD' {
			repl++
			continue
		}
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 {
			control++
		}
	}
	if total == 0 {
		return false
	}
	return control*20 > total || repl*50 > total
}

// reconcileToolHistory drops orphaned tool calls/results so resumed transcripts
// remain valid even if the original run stopped mid-tool batch.
func reconcileToolHistory(msgs []providers.Message) []providers.Message {
	toolResults := make(map[string]bool)
	for _, msg := range msgs {
		if msg.Role == "tool" && strings.TrimSpace(msg.ToolCallID) != "" {
			toolResults[msg.ToolCallID] = true
		}
	}

	keptToolCalls := make(map[string]bool)
	out := make([]providers.Message, 0, len(msgs))
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			if len(msg.ToolCalls) == 0 {
				out = append(out, msg)
				continue
			}
			filtered := make([]providers.ToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				if toolResults[tc.ID] {
					filtered = append(filtered, tc)
					keptToolCalls[tc.ID] = true
				}
			}
			if len(filtered) == 0 && strings.TrimSpace(msg.Content) == "" {
				continue
			}
			msg.ToolCalls = filtered
			out = append(out, msg)
		case "tool":
			if keptToolCalls[msg.ToolCallID] {
				out = append(out, msg)
			}
		default:
			out = append(out, msg)
		}
	}
	return out
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
	// Strip trailing /trace.jsonl so users can pass the full path
	runID = strings.TrimSuffix(runID, "/trace.jsonl")
	runID = strings.TrimSuffix(runID, string(filepath.Separator)+"trace.jsonl")

	// Try runs/<runID> first
	candidate := filepath.Join("runs", runID)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Search nested run directories under runs/
	var nested string
	_ = filepath.WalkDir("runs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || nested != "" || !d.IsDir() {
			return nil
		}
		if filepath.Base(path) != runID {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "trace.jsonl")); err == nil {
			nested = path
			return fs.SkipAll
		}
		return nil
	})
	if nested != "" {
		return nested, nil
	}
	// Try exact path
	if _, err := os.Stat(runID); err == nil {
		return runID, nil
	}
	return "", fmt.Errorf("run %q not found (checked runs/%s, nested runs/**/%s, and exact path)", runID, runID, runID)
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
	case "gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano", "gpt-5.3-codex":
		return "gpt-5.4", true
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
	defer func() { _ = f.Close() }()
	_, _ = f.Read(b)
	return b
}
