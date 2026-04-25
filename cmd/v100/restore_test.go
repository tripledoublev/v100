package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestResolveCheckpointForRestoreUsesLatestByDefault(t *testing.T) {
	runDir := t.TempDir()
	first := core.Checkpoint{
		ID:         "snap-1",
		SnapshotID: "snap-1",
		CreatedAt:  time.Unix(10, 0).UTC(),
	}
	second := core.Checkpoint{
		ID:         "snap-2",
		SnapshotID: "snap-2",
		CreatedAt:  time.Unix(20, 0).UTC(),
	}
	if err := core.PersistCheckpoint(runDir, first); err != nil {
		t.Fatal(err)
	}
	if err := core.PersistCheckpoint(runDir, second); err != nil {
		t.Fatal(err)
	}

	cp, err := resolveCheckpointForRestore(runDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID != "snap-2" {
		t.Fatalf("latest checkpoint id = %q, want snap-2", cp.ID)
	}
}

func TestReconstructHistoryResetsOnSandboxRestore(t *testing.T) {
	runDir := t.TempDir()
	checkpoint := core.Checkpoint{
		ID:         "snap-restore",
		SnapshotID: "snap-restore",
		CreatedAt:  time.Now().UTC(),
		Messages: []providers.Message{
			{Role: "user", Content: "before restore"},
			{
				Role:    "assistant",
				Content: "checkpoint state",
				ToolCalls: []providers.ToolCall{
					{ID: "call-1", Name: "fs_read", Args: json.RawMessage(`{"path":"README.md"}`)},
				},
			},
		},
		StepCount: 2,
	}
	if err := core.PersistCheckpoint(runDir, checkpoint); err != nil {
		t.Fatal(err)
	}

	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "mock",
			Model:     "test",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{Content: "old user"}),
		mustEvent(t, core.EventModelResp, core.ModelRespPayload{Text: "old assistant"}),
		mustEvent(t, core.EventSandboxRestore, core.SandboxRestorePayload{
			SnapshotID: "snap-restore",
			Method:     "full_copy",
			Reason:     "manual_restore",
		}),
		mustEvent(t, core.EventToolResult, core.ToolResultPayload{
			CallID: "call-1",
			Name:   "fs_read",
			OK:     true,
			Output: "after restore tool output",
		}),
	}

	msgs, providerName, model, workspace, _ := reconstructHistory(runDir, events)
	if providerName != "mock" || model != "test" || workspace != "/workspace" {
		t.Fatalf("unexpected run info: provider=%q model=%q workspace=%q", providerName, model, workspace)
	}
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3 (%+v)", len(msgs), msgs)
	}
	if msgs[0].Content != "before restore" || msgs[1].Content != "checkpoint state" {
		t.Fatalf("restore did not reset message history: %+v", msgs)
	}
	if msgs[2].Content != "after restore tool output" || msgs[2].Role != "tool" {
		t.Fatalf("unexpected post-restore message: %+v", msgs[2])
	}
}

func TestReconstructHistoryDropsIncompleteToolCallsOnResume(t *testing.T) {
	runDir := t.TempDir()
	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "codex",
			Model:     "gpt-5.4",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{Content: "inspect latest run"}),
		mustEvent(t, core.EventModelResp, core.ModelRespPayload{
			Text: "I'll inspect the latest run.",
			ToolCalls: []core.ToolCall{
				{ID: "call-1", Name: "fs_list", ArgsJSON: `{"path":"runs"}`},
				{ID: "call-2", Name: "project_search", ArgsJSON: `{"pattern":"gemini","path":"runs"}`},
			},
		}),
		mustEvent(t, core.EventToolResult, core.ToolResultPayload{
			CallID: "call-1",
			Name:   "fs_list",
			OK:     true,
			Output: `{"entries":["run-a/"]}`,
		}),
	}

	msgs, providerName, model, workspace, _ := reconstructHistory(runDir, events)
	if providerName != "codex" || model != "gpt-5.4" || workspace != "/workspace" {
		t.Fatalf("unexpected run info: provider=%q model=%q workspace=%q", providerName, model, workspace)
	}
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3 (%+v)", len(msgs), msgs)
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool calls = %d, want 1 (%+v)", len(msgs[1].ToolCalls), msgs[1].ToolCalls)
	}
	if msgs[1].ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant kept wrong tool call: %+v", msgs[1].ToolCalls[0])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call-1" {
		t.Fatalf("unexpected tool message after reconcile: %+v", msgs[2])
	}
}

func TestReconstructHistoryKeepsReviewerMessageAsSystemContext(t *testing.T) {
	runDir := t.TempDir()
	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "minimax",
			Model:     "MiniMax-M2.7",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{
			Content: "[external review: claude]\nsecond opinion",
			Source:  "reviewer",
		}),
	}

	msgs, _, _, _, _ := reconstructHistory(runDir, events)
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1 (%+v)", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "second opinion") {
		t.Fatalf("reviewer message = %+v, want system context", msgs[0])
	}
}

func TestReconstructHistorySanitizesBinaryToolOutputOnResume(t *testing.T) {
	runDir := t.TempDir()
	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "minimax",
			Model:     "MiniMax-M2.7",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{Content: "look at this image"}),
		mustEvent(t, core.EventModelResp, core.ModelRespPayload{
			ToolCalls: []core.ToolCall{
				{ID: "call-1", Name: "curl_fetch", ArgsJSON: `{"url":"https://example.com/image.png"}`},
			},
		}),
		mustEvent(t, core.EventToolResult, core.ToolResultPayload{
			CallID: "call-1",
			Name:   "curl_fetch",
			OK:     true,
			Output: "url: https://example.com/image.png\nstatus: 200\ncontent_type: image/png\n\n\x89PNG\r\n\x1a\n\x00\x00binary payload",
		}),
	}

	msgs, _, _, _, _ := reconstructHistory(runDir, events)
	if len(msgs) != 3 {
		t.Fatalf("message count = %d, want 3 (%+v)", len(msgs), msgs)
	}
	if got := msgs[2].Content; !strings.Contains(got, "[non-text response omitted during resume: image/png]") {
		t.Fatalf("expected sanitized resume content, got %q", got)
	}
	if strings.Contains(msgs[2].Content, "binary payload") {
		t.Fatalf("expected binary payload to be removed, got %q", msgs[2].Content)
	}
}

func TestResumeReplayEventsDropsHistoricalRunEnd(t *testing.T) {
	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{Provider: "codex", Model: "gpt-5.4"}),
		mustEvent(t, core.EventUserMsg, core.UserMsgPayload{Content: "resume this"}),
		mustEvent(t, core.EventRunEnd, core.RunEndPayload{Reason: "user_exit"}),
	}

	filtered := resumeReplayEvents(events)
	if len(filtered) != 2 {
		t.Fatalf("filtered event count = %d, want 2 (%+v)", len(filtered), filtered)
	}
	for _, ev := range filtered {
		if ev.Type == core.EventRunEnd {
			t.Fatalf("historical run.end should not be replayed: %+v", filtered)
		}
	}
}

func TestBuildResumeSummaryCapturesLatestGoalStateAndTools(t *testing.T) {
	events := []core.Event{
		mustEvent(t, core.EventRunStart, core.RunStartPayload{
			Provider:  "codex",
			Model:     "gpt-5.4",
			Workspace: "/workspace",
		}),
		mustEvent(t, core.EventRunEnd, core.RunEndPayload{
			Reason:  "user_exit",
			Summary: "Paused after inspecting traces and narrowing the bug to resume handling.",
		}),
	}
	msgs := []providers.Message{
		{Role: "user", Content: "fix resume so it continues from the last state"},
		{Role: "assistant", Content: "I isolated the issue to stale replayed run endings and weak resume context."},
		{Role: "tool", Name: "git_status", Content: "On branch main"},
		{Role: "tool", Name: "fs_read", Content: "cmd/v100/cmd_resume.go"},
	}

	got := buildResumeSummary("run-123", events, msgs)
	for _, want := range []string{
		"Resume summary for run run-123.",
		"Previous run ended with reason=user_exit.",
		"Current user goal: fix resume so it continues from the last state",
		"Last assistant state: I isolated the issue to stale replayed run endings and weak resume context.",
		"Recent successful tool results: git_status -> On branch main; fs_read -> cmd/v100/cmd_resume.go.",
		"Avoid re-reading broad repo context unless the prior summary is insufficient",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resume summary missing %q:\n%s", want, got)
		}
	}
}

func mustEvent(t *testing.T, typ core.EventType, payload any) core.Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		Type:    typ,
		Payload: b,
	}
}

func TestCheckpointStorePathSanitizesID(t *testing.T) {
	runDir := t.TempDir()
	cp := core.Checkpoint{
		ID:         "snap/with/slash",
		SnapshotID: "snap/with/slash",
		CreatedAt:  time.Now().UTC(),
	}
	if err := core.PersistCheckpoint(runDir, cp); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ReadCheckpoint(runDir, cp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Abs(runDir); err != nil {
		t.Fatal(err)
	}
}
