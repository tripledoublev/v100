package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
	"github.com/tripledoublev/v100/internal/policy"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/tools"
)

// RunComponents holds all the pieces needed to start a core.Loop.
type RunComponents struct {
	Config        *config.Config
	Run           *core.Run
	Provider      providers.Provider
	EmbedProvider providers.Provider
	Registry      *tools.Registry
	Policy        *policy.Policy
	Trace         *core.TraceWriter
	Budget        *core.BudgetTracker
	Session       executor.Session
	Mapper        *core.PathMapper
	Workspace     string
	Model         string
	ModelMetadata providers.ModelMetadata
	GenParams     providers.GenParams
	Solver        core.Solver
}

// BuildRunComponents constructs the agent runtime based on config and overrides.
// This is shared between CLI 'run' and ACP server modes.
func BuildRunComponents(cfg *config.Config, opts RunOptions) (*RunComponents, error) {
	// 1. Resolve Provider and dedicated Embedding Provider
	prov, err := buildProvider(cfg, cfg.Defaults.Provider)
	if err != nil {
		return nil, err
	}

	var embedProv providers.Provider
	embedProvName := opts.EmbeddingProvider
	if embedProvName == "" {
		embedProvName = cfg.Embedding.Provider
	}
	if embedProvName != "" {
		embedProv, err = buildProvider(cfg, embedProvName)
		if err != nil {
			return nil, fmt.Errorf("embedding provider %q: %w", embedProvName, err)
		}
	}

	// 2. Resolve Model
	model := opts.Model
	if model == "" {
		if pc, ok := cfg.Providers[cfg.Defaults.Provider]; ok {
			model = pc.DefaultModel
		}
	}

	// 3. Setup Run Directory and Meta
	runID := newRunID()
	runBase := opts.RunDir
	if runBase == "" {
		runBase = "runs"
	}
	runDir := filepath.Join(runBase, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create run dir: %w", err)
	}
	_ = os.MkdirAll(filepath.Join(runDir, "artifacts"), 0o755)

	sourceWorkspace := resolveWorkspace(opts.Workspace, runDir)
	meta := core.RunMeta{
		RunID:           runID,
		Name:            opts.Name,
		Tags:            opts.Tags,
		Provider:        prov.Name(),
		Model:           model,
		SourceWorkspace: sourceWorkspace,
		Sandbox:         cfg.Sandbox,
		CreatedAt:       time.Now().UTC(),
	}
	if cfg.Sandbox.Enabled {
		if fp, err := core.WorkspaceFingerprint(sourceWorkspace); err == nil {
			meta.SourceFingerprint = fp
		}
	}
	_ = core.WriteMeta(runDir, meta)

	// 4. Trace and Run object
	tracePath := filepath.Join(runDir, "trace.jsonl")
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		return nil, err
	}

	run := &core.Run{
		ID:        runID,
		Dir:       runDir,
		TraceFile: tracePath,
		Budget: core.Budget{
			MaxSteps:   cfg.Defaults.BudgetSteps,
			MaxTokens:  cfg.Defaults.BudgetTokens,
			MaxCostUSD: cfg.Defaults.BudgetCostUSD,
		},
	}

	// 5. Sandbox and Workspace
	session, mapper, workspace, err := buildSandboxSession(cfg, runID, sourceWorkspace, runBase)
	if err != nil {
		_ = trace.Close()
		return nil, err
	}

	// 6. Registry, Policy, and Budget
	reg := buildToolRegistry(cfg)
	if err := validateToolRegistry(reg); err != nil {
		_ = trace.Close()
		if cfg.Sandbox.Enabled {
			_ = session.Close()
		}
		return nil, err
	}

	pol := loadPolicy(cfg, opts.PolicyName)
	if opts.ToolTimeoutMS > 0 {
		pol.ToolTimeoutMS = opts.ToolTimeoutMS
	}
	if opts.DisableWatchdogs {
		pol.DisableWatchdogs = true
	}
	if cfg.Defaults.ContextLimit > 0 {
		pol.ContextLimit = cfg.Defaults.ContextLimit
	}
	if opts.Streaming != nil {
		pol.Streaming = *opts.Streaming
	}

	budget := core.NewBudgetTracker(&run.Budget)

	// 7. Solver
	solver, err := buildSolver(cfg, opts.SolverName)
	if err != nil {
		_ = trace.Close()
		if cfg.Sandbox.Enabled {
			_ = session.Close()
		}
		return nil, err
	}

	return &RunComponents{
		Config:        cfg,
		Run:           run,
		Provider:      prov,
		EmbedProvider: embedProv,
		Registry:      reg,
		Policy:        pol,
		Trace:         trace,
		Budget:        budget,
		Session:       session,
		Mapper:        mapper,
		Workspace:     workspace,
		Model:         model,
		Solver:        solver,
	}, nil
}

// RunOptions defines overrides for building run components.
type RunOptions struct {
	Model             string
	EmbeddingProvider string
	PolicyName        string
	RunDir            string
	Workspace         string
	SolverName        string
	ToolTimeoutMS     int
	DisableWatchdogs  bool
	Streaming         *bool
	Name              string
	Tags              map[string]string
}
