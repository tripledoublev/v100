package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ResearchRunner orchestrates autonomous experiment loops.
type ResearchRunner struct {
	Config   *ResearchConfig
	Dir      string // working directory for the experiments
	OutFn    func(format string, args ...interface{}) // logging output
	StopCh   chan struct{}
	maxRound int // 0 = unlimited
}

// NewResearchRunner creates a new research runner.
func NewResearchRunner(cfg *ResearchConfig, dir string, outFn func(string, ...interface{})) *ResearchRunner {
	if outFn == nil {
		outFn = func(_ string, _ ...interface{}) {}
	}
	return &ResearchRunner{
		Config: cfg,
		Dir:    dir,
		OutFn:  outFn,
		StopCh: make(chan struct{}),
	}
}

// SetMaxRounds limits the number of experiment rounds (0 = unlimited).
func (r *ResearchRunner) SetMaxRounds(n int) {
	r.maxRound = n
}

// Run executes the full research loop until Stop is called or max rounds reached.
func (r *ResearchRunner) Run(ctx context.Context) error {
	tag := time.Now().Format("jan02")
	branchName := fmt.Sprintf("%s/%s", r.Config.BranchPrefix, tag)

	// Setup: create branch, verify data, init results.tsv
	if err := r.setup(ctx, branchName); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	r.OutFn("=== Starting research loop on branch %s ===", branchName)

	baselineMetric := 0.0
	baselineSet := false

	round := 0
	for {
		select {
		case <-r.StopCh:
			r.OutFn("=== Stopped by user ===")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		round++
		if r.maxRound > 0 && round > r.maxRound {
			r.OutFn("=== Reached max rounds (%d) ===", r.maxRound)
			return nil
		}

		// 1. Run the experiment
		r.OutFn("--- Round %d ---", round)
		result, err := r.runExperiment(ctx, round)
		if err != nil {
			r.OutFn("experiment error (round %d): %v", round, err)
			continue
		}

		// 2. Record result
		if err := r.appendResult(result); err != nil {
			r.OutFn("failed to append result: %v", err)
		}

		// 3. Decide keep/discard
		if !baselineSet {
			baselineSet = true
			baselineMetric = result.Metric
			result.Status = "keep"
			r.OutFn("baseline: %s = %.6f", r.Config.Experiment.Metric, result.Metric)
		} else {
			result.Status = r.decide(result.Metric, baselineMetric)
			if result.Status == "keep" {
				baselineMetric = result.Metric
				r.OutFn("improved! %s = %.6f (was %.6f)", r.Config.Experiment.Metric, result.Metric, baselineMetric)
			} else {
				r.OutFn("no improvement: %s = %.6f (keep %.6f)", r.Config.Experiment.Metric, result.Metric, baselineMetric)
				// Revert to previous commit
				if err := r.revert(ctx); err != nil {
					r.OutFn("revert failed: %v", err)
				}
			}
		}

		// Update status in TSV
		if err := r.updateResultStatus(result.Commit, result.Status); err != nil {
			r.OutFn("failed to update status: %v", err)
		}
	}
}

// Stop signals the runner to halt after the current round.
func (r *ResearchRunner) Stop() {
	close(r.StopCh)
}

// --- Setup helpers ---

func (r *ResearchRunner) setup(ctx context.Context, branchName string) error {
	// Create branch
	if out, err := r.git(ctx, "checkout", "-b", branchName); err != nil {
		return fmt.Errorf("create branch %s: %w\n%s", branchName, err, out)
	}

	// Verify data exists
	cacheDir := os.ExpandEnv("$HOME/.cache/" + r.Config.Name)
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		r.OutFn("WARNING: cache dir %s not found — user may need to run prepare step", cacheDir)
	}

	// Init results.tsv
	resultsPath := r.resultsPath()
	if _, err := os.Stat(resultsPath); os.IsNotExist(err) {
		f, err := os.Create(resultsPath)
		if err != nil {
			return fmt.Errorf("create results.tsv: %w", err)
		}
		if _, err := fmt.Fprintf(f, "commit\t%s\tmemory_gb\tstatus\tdescription\n", r.Config.Experiment.Metric); err != nil {
			_ = f.Close()
			return fmt.Errorf("write header to results.tsv: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close results.tsv: %w", err)
		}
		r.OutFn("initialized %s", resultsPath)
	}

	return nil
}

// --- Experiment execution ---

func (r *ResearchRunner) runExperiment(ctx context.Context, round int) (*ResearchResult, error) {
	// Get current commit before running
	commitHash, _, err := r.gitInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("git info: %w", err)
	}

	// Parse timeout
	timeout := 5 * time.Minute
	if r.Config.Experiment.Timeout != "" {
		if d, err := time.ParseDuration(r.Config.Experiment.Timeout); err == nil {
			timeout = d
		}
	}

	// Build command
	cmd := exec.CommandContext(ctx, "sh", "-c", r.Config.Experiment.Command)
	cmd.Dir = r.Dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	r.OutFn("running: %s", r.Config.Experiment.Command)

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	var runErr error
	select {
	case err := <-done:
		runErr = err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		runErr = fmt.Errorf("timeout after %v", timeout)
	}

	// Parse output for metric and memory
	metric, memGB := r.parseOutput(round)
	if metric == 0 && runErr != nil {
		return &ResearchResult{
			Commit:      commitHash,
			Metric:      0,
			MemoryGB:    0,
			Status:      "crash",
			Description: fmt.Sprintf("round %d error: %v", round, runErr),
		}, nil
	}

	return &ResearchResult{
		Commit:      commitHash,
		Metric:      metric,
		MemoryGB:    memGB,
		Status:      "keep", // tentative
		Description: fmt.Sprintf("round %d", round),
	}, nil
}

// parseOutput greps the log file (run.log) for the metric and memory values.
// The experiment command should write output to run.log in the working dir.
func (r *ResearchRunner) parseOutput(round int) (metric, memGB float64) {
	logPath := r.Dir + "/run.log"
	f, err := os.Open(logPath)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()

	metricRE := regexp.MustCompile(`(?i)^` + r.Config.Experiment.Metric + `:\s*([0-9.]+)`)
	memRE := regexp.MustCompile(`(?i)^peak_vram_mb:\s*([0-9.]+)`)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if m := metricRE.FindStringSubmatch(line); len(m) > 1 {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				metric = v
			}
		}
		if m := memRE.FindStringSubmatch(line); len(m) > 1 {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				memGB = v / 1024.0
			}
		}
	}
	return metric, memGB
}

// --- Decision logic ---

func (r *ResearchRunner) decide(current, baseline float64) string {
	if r.Config.Experiment.Direction == "lower" {
		if current < baseline {
			return "keep"
		}
	} else { // higher
		if current > baseline {
			return "keep"
		}
	}
	return "discard"
}

// --- Git helpers ---

func (r *ResearchRunner) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (r *ResearchRunner) gitInfo(ctx context.Context) (hash, branch string, err error) {
	out, err := r.git(ctx, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", "", err
	}
	hash = strings.TrimSpace(out)

	out, err = r.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return hash, "", nil
	}
	branch = strings.TrimSpace(out)
	return hash, branch, nil
}

func (r *ResearchRunner) revert(ctx context.Context) error {
	// git reset --hard HEAD^
	out, err := r.git(ctx, "reset", "--hard", "HEAD^")
	if err != nil {
		return fmt.Errorf("reset: %w\n%s", err, out)
	}
	return nil
}

/*
func (r *ResearchRunner) commit(ctx context.Context, msg string) error {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	out, err := r.git(ctx, "commit", "-m", msg)
	if err != nil {
		return fmt.Errorf("commit: %w\n%s", err, out)
	}
	return nil
}
*/

// --- Results persistence ---

func (r *ResearchRunner) resultsPath() string {
	return r.Dir + "/results.tsv"
}

func (r *ResearchRunner) appendResult(res *ResearchResult) error {
	f, err := os.OpenFile(r.resultsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = fmt.Fprintf(f, "%s\t%.6f\t%.1f\t%s\t%s\n",
		res.Commit, res.Metric, res.MemoryGB, res.Status, res.Description)
	return err
}

func (r *ResearchRunner) updateResultStatus(commit, status string) error {
	// Read entire file, update the row, rewrite
	path := r.resultsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, commit+"\t") {
			parts := strings.Split(line, "\t")
			if len(parts) >= 4 {
				parts[3] = status
				lines[i] = strings.Join(parts, "\t")
			}
			break
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// ParseMetric extracts the metric value from command output using the configured pattern.
func ParseMetric(output []byte, metricName string) float64 {
	re := regexp.MustCompile(`(?i)^` + metricName + `:\s*([0-9.]+)`)
	m := re.FindSubmatch(output)
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// ParseMemoryGB extracts peak VRAM in GB from output.
func ParseMemoryGB(output []byte) float64 {
	re := regexp.MustCompile(`(?i)^peak_vram_mb:\s*([0-9.]+)`)
	m := re.FindSubmatch(output)
	if len(m) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v / 1024.0
}

// --- Agent integration helpers ---

// InjectProgram reads the program file and returns its content for agent context.
func (r *ResearchRunner) InjectProgram() (string, error) {
	programPath := r.Dir + "/" + r.Config.Target.Program
	data, err := os.ReadFile(programPath)
	if err != nil {
		return "", fmt.Errorf("read program %s: %w", programPath, err)
	}
	return string(data), nil
}

// InjectContext reads additional context files for the agent.
func (r *ResearchRunner) InjectContext() ([]string, error) {
	var files []string
	for _, f := range r.Config.Target.Context {
		path := r.Dir + "/" + f
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip missing context files
		}
		files = append(files, string(data))
	}
	return files, nil
}

// SnapshotResults reads the current results.tsv content.
func (r *ResearchRunner) SnapshotResults() (string, error) {
	data, err := os.ReadFile(r.resultsPath())
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Convenience runners ---

// RunSingle runs one experiment and returns the result. Useful for testing.
func RunSingle(ctx context.Context, cfg *ResearchConfig, dir string) (*ResearchResult, error) {
	runner := NewResearchRunner(cfg, dir, nil)
	runner.SetMaxRounds(1)

	tag := time.Now().Format("jan02")
	branchName := fmt.Sprintf("%s/%s", cfg.BranchPrefix, tag)

	if err := runner.setup(ctx, branchName); err != nil {
		return nil, err
	}

	return runner.runExperiment(ctx, 1)
}

// BufferLogger returns an OutputFn that collects output into a bytes.Buffer.
func BufferLogger() (*bytes.Buffer, func(string, ...interface{})) {
	var buf bytes.Buffer
	fn := func(format string, args ...interface{}) {
		fmt.Fprintf(&buf, format+"\n", args...)
	}
	return &buf, fn
}
