package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/tripledoublev/v100/internal/compute"
)

// ExperimentRunContext describes one concrete experiment invocation.
type ExperimentRunContext struct {
	Round      int
	RunID      string
	Commit     string
	Branch     string
	Workspace  string
	TargetFile string
	MetricName string
	Timestamp  time.Time
}

// ExperimentExecutionResult contains the parsed outcome of an experiment run.
type ExperimentExecutionResult struct {
	Metric    float64
	MemoryGB  float64
	Status    string
	Output    string
	LocalLog  string
	Command   string
	Setup     string
	Collect   string
	StartedAt time.Time
	EndedAt   time.Time
}

func RunResearchExperiment(ctx context.Context, cfg *ResearchConfig, runCtx ExperimentRunContext, computeProv compute.Provider) (*ExperimentExecutionResult, error) {
	if computeProv == nil {
		computeProv = compute.NewLocalProvider()
	}
	timeout := 10 * time.Minute
	if cfg.Experiment.Timeout != "" {
		if dur, err := time.ParseDuration(cfg.Experiment.Timeout); err == nil {
			timeout = dur
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startedAt := time.Now()
	templateData := newExperimentTemplateData(cfg, runCtx)
	env := buildExperimentEnv(cfg, templateData)
	workDir := normalizedResearchWorkDir(cfg)
	if !filepath.IsAbs(workDir) && strings.TrimSpace(runCtx.Workspace) != "" {
		workDir = filepath.Join(runCtx.Workspace, workDir)
	}

	result := &ExperimentExecutionResult{
		Status:    "completed",
		StartedAt: startedAt,
	}

	var combined bytes.Buffer

	runHook := func(command string) (string, error) {
		rendered, err := renderResearchTemplate(command, templateData)
		if err != nil {
			return "", err
		}
		rendered = strings.TrimSpace(rendered)
		if rendered == "" {
			return "", nil
		}
		var out bytes.Buffer
		_, execErr := computeProv.Execute(ctx, compute.ExecuteRequest{
			Command: rendered,
			WorkDir: workDir,
			Env:     env,
			Stdout:  &out,
			Stderr:  &out,
		})
		combined.Write(out.Bytes())
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			combined.WriteByte('\n')
		}
		return rendered, execErr
	}

	var err error
	if cfg.Experiment.Setup != "" {
		result.Setup, err = runHook(cfg.Experiment.Setup)
		if err != nil {
			result.Status = "crash"
			result.Output = combined.String()
			result.LocalLog = readExperimentLog(workDir, normalizedResearchLogFile(cfg))
			result.EndedAt = time.Now()
			return finalizeExperimentResult(cfg, result), nil
		}
	}

	result.Command, err = runHook(cfg.Experiment.Command)
	commandErr := err

	if cfg.Experiment.Collect != "" {
		result.Collect, err = runHook(cfg.Experiment.Collect)
		if commandErr == nil && err != nil {
			commandErr = err
		}
	}

	result.Output = combined.String()
	result.LocalLog = readExperimentLog(workDir, normalizedResearchLogFile(cfg))
	result.EndedAt = time.Now()
	if commandErr != nil {
		result.Status = "crash"
	}

	return finalizeExperimentResult(cfg, result), nil
}

type researchTemplateData struct {
	Round      int
	RunID      string
	Commit     string
	Branch     string
	TargetFile string
	Metric     string
	Timestamp  string
	UnixTime   int64
	LogFile    string
	WorkDir    string
	WandB      researchWandBTemplateData
}

type researchWandBTemplateData struct {
	Enabled bool
	Project string
	Entity  string
	Group   string
	JobType string
	RunName string
	Mode    string
	Tags    string
}

func newExperimentTemplateData(cfg *ResearchConfig, runCtx ExperimentRunContext) researchTemplateData {
	workDir := normalizedResearchWorkDir(cfg)
	logFile := normalizedResearchLogFile(cfg)
	wb := cfg.Experiment.Tracking.WandB
	runName := strings.TrimSpace(wb.RunName)
	if runName == "" {
		runName = fmt.Sprintf("%s-r%03d-%s", cfg.Name, runCtx.Round, runCtx.Commit)
	}
	data := researchTemplateData{
		Round:      runCtx.Round,
		RunID:      runCtx.RunID,
		Commit:     runCtx.Commit,
		Branch:     runCtx.Branch,
		TargetFile: runCtx.TargetFile,
		Metric:     runCtx.MetricName,
		Timestamp:  runCtx.Timestamp.UTC().Format(time.RFC3339),
		UnixTime:   runCtx.Timestamp.UTC().Unix(),
		LogFile:    logFile,
		WorkDir:    workDir,
		WandB: researchWandBTemplateData{
			Enabled: wb.Enabled,
			Project: wb.Project,
			Entity:  wb.Entity,
			Group:   wb.Group,
			JobType: wb.JobType,
			RunName: runName,
			Mode:    wb.Mode,
			Tags:    strings.Join(wb.Tags, ","),
		},
	}
	if rendered, err := renderResearchTemplate(runName, data); err == nil && strings.TrimSpace(rendered) != "" {
		data.WandB.RunName = strings.TrimSpace(rendered)
	}
	return data
}

func renderResearchTemplate(raw string, data researchTemplateData) (string, error) {
	tmpl, err := template.New("research").Option("missingkey=zero").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse experiment template: %w", err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render experiment template: %w", err)
	}
	return out.String(), nil
}

func buildExperimentEnv(cfg *ResearchConfig, data researchTemplateData) []string {
	envMap := map[string]string{}
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if ok {
			envMap[key] = value
		}
	}
	for key, value := range cfg.Experiment.Env {
		rendered, err := renderResearchTemplate(value, data)
		if err != nil {
			rendered = value
		}
		envMap[key] = rendered
	}

	envMap["RESEARCH_NAME"] = cfg.Name
	envMap["RESEARCH_ROUND"] = strconv.Itoa(data.Round)
	envMap["RESEARCH_RUN_ID"] = data.RunID
	envMap["RESEARCH_COMMIT"] = data.Commit
	envMap["RESEARCH_BRANCH"] = data.Branch
	envMap["RESEARCH_TARGET_FILE"] = data.TargetFile
	envMap["RESEARCH_METRIC"] = data.Metric
	envMap["RESEARCH_LOG_FILE"] = data.LogFile
	envMap["RESEARCH_TIMESTAMP"] = data.Timestamp

	if wb := cfg.Experiment.Tracking.WandB; wb.Enabled {
		if wb.Project != "" {
			envMap["WANDB_PROJECT"] = wb.Project
		}
		if wb.Entity != "" {
			envMap["WANDB_ENTITY"] = wb.Entity
		}
		if wb.Mode != "" {
			envMap["WANDB_MODE"] = wb.Mode
		}
		if wb.Group != "" {
			envMap["WANDB_RUN_GROUP"] = wb.Group
		}
		if data.WandB.RunName != "" {
			envMap["WANDB_NAME"] = data.WandB.RunName
		}
		if wb.JobType != "" {
			envMap["WANDB_JOB_TYPE"] = wb.JobType
		}
		if len(wb.Tags) > 0 {
			envMap["WANDB_TAGS"] = strings.Join(wb.Tags, ",")
		}
		if wb.Notes != "" {
			envMap["WANDB_NOTES"] = wb.Notes
		}
		if wb.BaseURL != "" {
			envMap["WANDB_BASE_URL"] = wb.BaseURL
		}
		if wb.GitAutoDisable {
			envMap["WANDB_DISABLE_GIT"] = "true"
		}
		if wb.APIKeyEnv != "" {
			if apiKey := os.Getenv(wb.APIKeyEnv); apiKey != "" {
				envMap["WANDB_API_KEY"] = apiKey
			}
		}
	}

	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	// Deterministic output helps tests.
	sortStrings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func finalizeExperimentResult(cfg *ResearchConfig, result *ExperimentExecutionResult) *ExperimentExecutionResult {
	parseSource := result.Output
	if result.LocalLog != "" {
		parseSource += "\n" + result.LocalLog
	}
	metric, foundMetric := parseResearchMetric(parseSource, cfg.Experiment.Metric, cfg.Experiment.MetricRegex)
	result.Metric = metric
	result.MemoryGB = parseResearchMemoryGB(parseSource, cfg.Experiment.MemoryRegex)
	if !foundMetric {
		result.Status = "crash"
	}
	return result
}

func parseResearchMetric(output, metricName, pattern string) (float64, bool) {
	reText := strings.TrimSpace(pattern)
	if reText == "" {
		reText = defaultMetricRegex(metricName)
	}
	re, err := regexp.Compile(reText)
	if err != nil {
		return 0, false
	}
	m := re.FindStringSubmatch(output)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func parseResearchMemoryGB(output, pattern string) float64 {
	reText := strings.TrimSpace(pattern)
	if reText == "" {
		reText = `(?im)^peak_vram_mb:\s*([0-9.]+)`
	}
	re, err := regexp.Compile(reText)
	if err != nil {
		return 0
	}
	m := re.FindStringSubmatch(output)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	return v / 1024.0
}

func defaultMetricRegex(metricName string) string {
	return `(?im)^` + regexp.QuoteMeta(metricName) + `:\s*([0-9.]+)`
}

func normalizedResearchWorkDir(cfg *ResearchConfig) string {
	workDir := strings.TrimSpace(cfg.Experiment.WorkDir)
	if workDir == "" {
		return "."
	}
	return workDir
}

func normalizedResearchLogFile(cfg *ResearchConfig) string {
	logFile := strings.TrimSpace(cfg.Experiment.LogFile)
	if logFile == "" {
		return "run.log"
	}
	return logFile
}

func readExperimentLog(workDir, logFile string) string {
	logFile = strings.TrimSpace(logFile)
	if logFile == "" {
		return ""
	}
	logPath := logFile
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(workDir, logPath)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
