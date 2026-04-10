package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunResearchExperimentWithHooksAndLogParsing(t *testing.T) {
	dir := t.TempDir()
	cfg := &ResearchConfig{
		Name: "autoresearch",
		Target: ResearchTarget{
			File: "train.py",
		},
		Experiment: ResearchExperiment{
			WorkDir: ".",
			LogFile: "run.log",
			Setup:   "printf 'setup {{.Round}} {{.Commit}}\\n' > pre.log",
			Command: "printf 'val_bpb: 0.875\\npeak_vram_mb: 2048\\n' > {{.LogFile}}",
			Collect: "cat pre.log",
			Metric:  "val_bpb",
		},
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	res, err := RunResearchExperiment(context.Background(), cfg, ExperimentRunContext{
		Round:      7,
		RunID:      "run-7",
		Commit:     "abc1234",
		Branch:     "research/apr08",
		TargetFile: "train.py",
		MetricName: "val_bpb",
		Timestamp:  time.Unix(1700000000, 0),
	}, nil)
	if err != nil {
		t.Fatalf("RunResearchExperiment: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status = %q, want completed", res.Status)
	}
	if res.Metric != 0.875 {
		t.Fatalf("metric = %v, want 0.875", res.Metric)
	}
	if res.MemoryGB != 2 {
		t.Fatalf("memory = %v, want 2", res.MemoryGB)
	}
	if !strings.Contains(res.Output, "setup 7 abc1234") {
		t.Fatalf("output %q does not contain setup hook output", res.Output)
	}
	if !strings.Contains(res.LocalLog, "val_bpb: 0.875") {
		t.Fatalf("local log %q missing metric", res.LocalLog)
	}
}

func TestRunResearchExperimentWandBEnv(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "env.txt")
	t.Setenv("TEST_WANDB_KEY", "secret123")

	cfg := &ResearchConfig{
		Name: "autoresearch",
		Target: ResearchTarget{
			File: "train.py",
		},
		Experiment: ResearchExperiment{
			WorkDir: ".",
			LogFile: "run.log",
			Command: "printf 'WANDB_PROJECT=%s\\nWANDB_NAME=%s\\nWANDB_API_KEY=%s\\nRESEARCH_COMMIT=%s\\n' \"$WANDB_PROJECT\" \"$WANDB_NAME\" \"$WANDB_API_KEY\" \"$RESEARCH_COMMIT\" > env.txt && printf 'val_bpb: 0.9\\n' > run.log",
			Metric:  "val_bpb",
			Tracking: ResearchTrackingConfig{
				WandB: ResearchWandBConfig{
					Enabled:   true,
					Project:   "proj",
					RunName:   "trial-{{.Round}}",
					APIKeyEnv: "TEST_WANDB_KEY",
				},
			},
		},
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	res, err := RunResearchExperiment(context.Background(), cfg, ExperimentRunContext{
		Round:      3,
		RunID:      "run-3",
		Commit:     "deadbee",
		Branch:     "research/apr08",
		TargetFile: "train.py",
		MetricName: "val_bpb",
		Timestamp:  time.Unix(1700000000, 0),
	}, nil)
	if err != nil {
		t.Fatalf("RunResearchExperiment: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status = %q, want completed", res.Status)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"WANDB_PROJECT=proj",
		"WANDB_NAME=trial-3",
		"WANDB_API_KEY=secret123",
		"RESEARCH_COMMIT=deadbee",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("env file %q missing %q", text, want)
		}
	}
}
