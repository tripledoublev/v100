package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func TestPulseCmdRegistered(t *testing.T) {
	cmd := rootCmd()
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "pulse" {
			found = true
			if child.Flags().Lookup("run-dir") == nil {
				t.Fatal("pulse command missing --run-dir flag")
			}
		}
	}
	if !found {
		t.Fatal("pulse command not registered")
	}
}

func TestBuildPulseLineUsesLatestActiveRun(t *testing.T) {
	runsDir := t.TempDir()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	writePulseRun(t, runsDir, "20260506T110000-deadbeef", []core.Event{
		pulseEvent(t, now.Add(-10*time.Minute), core.EventRunStart, "s1", core.RunStartPayload{Provider: "codex", Model: "gpt-5.4"}),
		pulseEvent(t, now.Add(-9*time.Minute), core.EventRunEnd, "s1", core.RunEndPayload{Reason: "completed"}),
	})
	writePulseRun(t, runsDir, "20260506T115000-cafebabe", []core.Event{
		pulseEvent(t, now.Add(-2*time.Minute), core.EventRunStart, "s1", core.RunStartPayload{Provider: "codex", Model: "gpt-5.4"}),
		pulseEvent(t, now.Add(-30*time.Second), core.EventToolCall, "s1", core.ToolCallPayload{Name: "project_search", Args: "{}"}),
	})

	line, err := buildPulseLine("", runsDir, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"20260506T115000-cafebabe", "active", "30s-ago", "running tool project_search"} {
		if !strings.Contains(line, want) {
			t.Fatalf("pulse line %q missing %q", line, want)
		}
	}
}

func TestBuildPulseLineReportsExplicitEndedRun(t *testing.T) {
	runsDir := t.TempDir()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	writePulseRun(t, runsDir, "20260506T110000-deadbeef", []core.Event{
		pulseEvent(t, now.Add(-2*time.Minute), core.EventRunStart, "s1", core.RunStartPayload{Provider: "codex"}),
		pulseEvent(t, now.Add(-1*time.Minute), core.EventRunEnd, "s1", core.RunEndPayload{Reason: "completed"}),
	})

	line, err := buildPulseLine(filepath.Join(runsDir, "20260506T110000-deadbeef"), "", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"20260506T110000-deadbeef", "ended", "1m-ago", "ended completed"} {
		if !strings.Contains(line, want) {
			t.Fatalf("pulse line %q missing %q", line, want)
		}
	}
}

func TestLatestActiveRunDirRejectsWhenNoneActive(t *testing.T) {
	runsDir := t.TempDir()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	writePulseRun(t, runsDir, "20260506T110000-deadbeef", []core.Event{
		pulseEvent(t, now.Add(-2*time.Minute), core.EventRunStart, "s1", core.RunStartPayload{}),
		pulseEvent(t, now.Add(-1*time.Minute), core.EventRunEnd, "s1", core.RunEndPayload{Reason: "completed"}),
	})
	_, err := latestActiveRunDir(runsDir)
	if err == nil {
		t.Fatal("expected no active runs error")
	}
	if !strings.Contains(err.Error(), "no active runs found") {
		t.Fatalf("error = %q, want no active runs", err)
	}
}

func writePulseRun(t *testing.T, runsDir, runID string, events []core.Event) {
	t.Helper()
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := core.RunMeta{RunID: runID, CreatedAt: events[0].TS}
	if err := core.WriteMeta(runDir, meta); err != nil {
		t.Fatal(err)
	}
	trace, err := core.OpenTrace(filepath.Join(runDir, "trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		ev.RunID = runID
		if err := trace.Write(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
}

func pulseEvent(t *testing.T, ts time.Time, typ core.EventType, stepID string, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		TS:      ts,
		StepID:  stepID,
		EventID: string(typ) + "-1",
		Type:    typ,
		Payload: b,
	}
}
