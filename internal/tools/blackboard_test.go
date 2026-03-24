package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlackboardReadWriteShareWorkspaceAcrossRuns(t *testing.T) {
	workspace := t.TempDir()
	sandbox1 := t.TempDir()
	sandbox2 := t.TempDir()

	args, err := json.Marshal(map[string]any{
		"content": "budget gap confirmed",
	})
	if err != nil {
		t.Fatal(err)
	}

	writeRes, err := BlackboardWrite().Exec(context.Background(), ToolCallContext{
		RunID:            "run-1",
		WorkspaceDir:     sandbox1,
		HostWorkspaceDir: workspace,
	}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !writeRes.OK {
		t.Fatalf("blackboard_write failed: %s", writeRes.Output)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "blackboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "budget gap confirmed") {
		t.Fatalf("workspace blackboard missing content: %q", string(data))
	}

	readRes, err := BlackboardRead().Exec(context.Background(), ToolCallContext{
		RunID:            "run-2",
		WorkspaceDir:     sandbox2,
		HostWorkspaceDir: workspace,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !readRes.OK {
		t.Fatalf("blackboard_read failed: %s", readRes.Output)
	}
	if !strings.Contains(readRes.Output, "budget gap confirmed") {
		t.Fatalf("blackboard_read output = %q, want shared content", readRes.Output)
	}
}

func TestAppendBlackboardDispatchUsesWorkspaceBlackboard(t *testing.T) {
	workspace := t.TempDir()

	err := appendBlackboardDispatch(workspace, "fanout", "researcher", "Map replay", AgentRunResult{
		OK:         true,
		Result:     "done",
		UsedSteps:  2,
		UsedTokens: 42,
		CostUSD:    0.01,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "blackboard.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "## Dispatch (fanout)") {
		t.Fatalf("blackboard entry missing dispatch header: %q", text)
	}
	if !strings.Contains(text, "- agent: researcher") {
		t.Fatalf("blackboard entry missing agent: %q", text)
	}
}
