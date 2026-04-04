package ui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
)

func mustPayload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

func testSyncDiff(t *testing.T) eval.SyncDiff {
	t.Helper()
	eventsA := []core.Event{
		{
			Type: core.EventRunStart,
			Payload: mustPayload(t, core.RunStartPayload{
				Provider: "openai",
				Model:    "gpt-5.4",
			}),
		},
		{
			Type: core.EventToolCall,
			Payload: mustPayload(t, core.ToolCallPayload{
				Name: "fs_read",
				Args: `{"path":"README.md"}`,
			}),
		},
		{
			Type: core.EventRunEnd,
			Payload: mustPayload(t, core.RunEndPayload{
				Reason:     "completed",
				UsedSteps:  2,
				UsedTokens: 42,
			}),
		},
	}
	eventsB := []core.Event{
		{
			Type: core.EventRunStart,
			Payload: mustPayload(t, core.RunStartPayload{
				Provider: "openai",
				Model:    "gpt-5.4",
			}),
		},
		{
			Type: core.EventToolCall,
			Payload: mustPayload(t, core.ToolCallPayload{
				Name: "fs_read",
				Args: `{"path":"docs/README.md"}`,
			}),
		},
		{
			Type: core.EventRunEnd,
			Payload: mustPayload(t, core.RunEndPayload{
				Reason:     "completed",
				UsedSteps:  2,
				UsedTokens: 42,
			}),
		},
	}
	return eval.SyncTraces("run-a", "run-b", eventsA, eventsB)
}

func TestNewDiffModel(t *testing.T) {
	sd := testSyncDiff(t)
	m := NewDiffModel(sd)
	if m == nil {
		t.Fatal("NewDiffModel returned nil")
	}
	if m.diff.RunA != "run-a" || m.diff.RunB != "run-b" {
		t.Error("diff not stored correctly")
	}
}

func TestDiffModelViewBeforeReady(t *testing.T) {
	m := NewDiffModel(testSyncDiff(t))
	out := m.View()
	if !strings.Contains(out, "Initializing") {
		t.Fatalf("expected initializing message, got %q", out)
	}
}

func TestDiffModelViewContainsTranscriptPayloads(t *testing.T) {
	sd := eval.SyncTraces("run-a", "run-b",
		[]core.Event{{
			Type: core.EventModelResp,
			Payload: mustPayload(t, core.ModelRespPayload{
				Text: "Inspect README and summarize the repo structure.",
				ToolCalls: []core.ToolCall{{
					Name:     "fs_read",
					ArgsJSON: `{"path":"README.md"}`,
				}},
				Usage: core.Usage{InputTokens: 12, OutputTokens: 24, CostUSD: 0.0012},
			}),
		}},
		[]core.Event{{
			Type: core.EventModelResp,
			Payload: mustPayload(t, core.ModelRespPayload{
				Text: "Inspect docs and summarize the repo structure.",
				ToolCalls: []core.ToolCall{{
					Name:     "fs_read",
					ArgsJSON: `{"path":"docs/README.md"}`,
				}},
				Usage: core.Usage{InputTokens: 12, OutputTokens: 24, CostUSD: 0.0012},
			}),
		}},
	)
	m := NewDiffModel(sd)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	out := m.View()
	if !strings.Contains(out, "Inspect README and summarize") {
		t.Fatalf("view missing model text: %q", out)
	}
	if !strings.Contains(out, `tool: fs_read({"path":"README.md"})`) {
		t.Fatalf("view missing tool args: %q", out)
	}
}

func TestBuildDiffPaneContentsPadsMultilineBlocks(t *testing.T) {
	sd := eval.SyncTraces("run-a", "run-b",
		[]core.Event{{
			Type: core.EventToolResult,
			Payload: mustPayload(t, core.ToolResultPayload{
				Name:   "sh",
				OK:     true,
				Output: "line 1\nline 2\nline 3\nline 4",
			}),
		}},
		[]core.Event{{
			Type: core.EventToolResult,
			Payload: mustPayload(t, core.ToolResultPayload{
				Name:   "sh",
				OK:     true,
				Output: "single line",
			}),
		}},
	)
	left, right, _ := buildDiffPaneContents(sd, 36, 36)
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	if len(leftLines) != len(rightLines) {
		t.Fatalf("pane line counts differ: left=%d right=%d", len(leftLines), len(rightLines))
	}
}

func TestDiffModelScrollSync(t *testing.T) {
	m := NewDiffModel(testSyncDiff(t))
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 18})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.leftPane.YOffset != m.rightPane.YOffset {
		t.Fatalf("panes should scroll in sync: left=%d right=%d", m.leftPane.YOffset, m.rightPane.YOffset)
	}
}

func TestDiffModelJumpToDivergenceUsesRenderedOffset(t *testing.T) {
	sd := eval.SyncTraces("run-a", "run-b",
		[]core.Event{
			{
				Type: core.EventUserMsg,
				Payload: mustPayload(t, core.UserMsgPayload{
					Content: "Line one of a longer message.\nLine two keeps the segment tall.",
				}),
			},
			{
				Type:    core.EventSolverPlan,
				Payload: mustPayload(t, map[string]string{"plan": "Read README then inspect cmd/v100."}),
			},
		},
		[]core.Event{
			{
				Type: core.EventUserMsg,
				Payload: mustPayload(t, core.UserMsgPayload{
					Content: "Line one of a longer message.\nLine two keeps the segment tall.",
				}),
			},
			{
				Type:    core.EventSolverPlan,
				Payload: mustPayload(t, map[string]string{"plan": "Read docs then inspect cmd/v100."}),
			},
		},
	)
	m := NewDiffModel(sd)
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 8})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if m.divergeYOffset <= 0 {
		t.Fatalf("expected multiline prefix to move divergence below row 0, got %d", m.divergeYOffset)
	}
	if m.leftPane.YOffset != m.divergeYOffset || m.rightPane.YOffset != m.divergeYOffset {
		t.Fatalf("divergence jump should use rendered line offset: left=%d right=%d want=%d",
			m.leftPane.YOffset, m.rightPane.YOffset, m.divergeYOffset)
	}
}

func TestRenderDiffEventBlockShowsPlanText(t *testing.T) {
	ev := core.Event{
		Type:    core.EventSolverPlan,
		Payload: mustPayload(t, map[string]string{"plan": "Inspect README and compare tool args."}),
	}
	lines := renderDiffEventBlock(&ev, 48)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Inspect README and compare tool args.") {
		t.Fatalf("plan text missing from block: %q", joined)
	}
}

func TestRenderDiffEventBlockShowsToolArgs(t *testing.T) {
	ev := core.Event{
		Type: core.EventToolCall,
		Payload: mustPayload(t, core.ToolCallPayload{
			Name: "fs_read",
			Args: `{"path":"README.md","offset":120}`,
		}),
	}
	lines := renderDiffEventBlock(&ev, 48)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, `args: {"path":"README.md","offset":120}`) {
		t.Fatalf("tool args missing from block: %q", joined)
	}
}

func TestRenderDiffHeader(t *testing.T) {
	sd := testSyncDiff(t)
	header := renderDiffHeader(sd, 120)
	if !strings.Contains(header, "run-a") || !strings.Contains(header, "run-b") {
		t.Fatalf("header should contain run IDs: %q", header)
	}
}
