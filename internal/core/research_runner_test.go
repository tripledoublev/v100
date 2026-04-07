package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestResearchRunnerDecide(t *testing.T) {
	tests := []struct {
		name      string
		direction string
		current   float64
		baseline  float64
		want      string
	}{
		{"lower is better, improved", "lower", 0.9, 1.0, "keep"},
		{"lower is better, no change", "lower", 1.0, 1.0, "discard"},
		{"lower is better, worse", "lower", 1.1, 1.0, "discard"},
		{"higher is better, improved", "higher", 1.1, 1.0, "keep"},
		{"higher is better, no change", "higher", 1.0, 1.0, "discard"},
		{"higher is better, worse", "higher", 0.9, 1.0, "discard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ResearchConfig{
				Experiment: ResearchExperiment{Direction: tt.direction},
			}
			runner := &ResearchRunner{Config: cfg}
			got := runner.decide(tt.current, tt.baseline)
			if got != tt.want {
				t.Errorf("decide(%v, %v) = %q, want %q", tt.current, tt.baseline, got, tt.want)
			}
		})
	}
}

func TestParseMetric(t *testing.T) {
	tests := []struct {
		name   string
		output []byte
		metric string
		want   float64
	}{
		{"val_bpb", []byte("val_bpb:          0.997900\n"), "val_bpb", 0.9979},
		{"accuracy", []byte("accuracy: 0.85\n"), "accuracy", 0.85},
		{"missing", []byte("loss: 0.5\n"), "val_bpb", 0},
		{"case insensitive", []byte("VAL_BPB: 0.99\n"), "val_bpb", 0.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMetric(tt.output, tt.metric)
			if got != tt.want {
				t.Errorf("ParseMetric(%q, %q) = %v, want %v", tt.output, tt.metric, got, tt.want)
			}
		})
	}
}

func TestParseMemoryGB(t *testing.T) {
	tests := []struct {
		name   string
		output []byte
		want   float64
	}{
		{"45GB", []byte("peak_vram_mb:     45060.2\n"), 44.0},
		{"12GB", []byte("peak_vram_mb: 12288.0\n"), 12.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMemoryGB(tt.output)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.01 {
				t.Errorf("ParseMemoryGB(%q) = %v, want ~%v", tt.output, got, tt.want)
			}
		})
	}
}

func TestRunSingle(t *testing.T) {
	// Create a temp dir with minimal git repo
	dir := t.TempDir()

	// Init git repo
	runShell(t, dir, "git init")
	runShell(t, dir, "git config user.email test@test.com")
	runShell(t, dir, "git config user.name Test")

	// Create a dummy file and commit
	dummy := filepath.Join(dir, "train.py")
	if err := os.WriteFile(dummy, []byte("print('hello')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runShell(t, dir, "git add .")
	runShell(t, dir, "git commit -m initial")

	cfg := &ResearchConfig{
		Name:         "test",
		BranchPrefix: "test",
		Target:       ResearchTarget{File: "train.py", Program: "program.md"},
		Experiment: ResearchExperiment{
			Command:   "echo 'val_bpb: 0.99' > run.log",
			Timeout:   "10s",
			Metric:    "val_bpb",
			Direction: "lower",
		},
		Budget: ResearchBudget{Steps: 1},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := RunSingle(ctx, cfg, dir)
	if err != nil {
		t.Fatalf("RunSingle failed: %v", err)
	}

	if result.Metric != 0.99 {
		t.Errorf("metric = %v, want 0.99", result.Metric)
	}
}

func runShell(t *testing.T, dir, cmdStr string) {
	t.Helper()
	c := exec.Command("sh", "-c", cmdStr)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("shell %q failed in %s: %v\n%s", cmdStr, dir, err, string(out))
	}
}