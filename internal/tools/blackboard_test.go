package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

type blackboardEmbeddingProvider struct{}

func (p *blackboardEmbeddingProvider) Name() string { return "test" }
func (p *blackboardEmbeddingProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}
func (p *blackboardEmbeddingProvider) Complete(context.Context, providers.CompleteRequest) (providers.CompleteResponse, error) {
	return providers.CompleteResponse{}, nil
}
func (p *blackboardEmbeddingProvider) Embed(_ context.Context, _ providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{Embedding: []float32{1, 0}}, nil
}
func (p *blackboardEmbeddingProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

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

func TestBlackboardVectorStoreSharesWorkspaceAcrossRuns(t *testing.T) {
	workspace := t.TempDir()
	prov := &blackboardEmbeddingProvider{}

	args, err := json.Marshal(map[string]any{
		"content": "remember the replay artifact convention",
		"tags":    map[string]string{"scope": "workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}

	storeRes, err := BlackboardStore().Exec(context.Background(), ToolCallContext{
		RunID:            "run-1",
		WorkspaceDir:     t.TempDir(),
		HostWorkspaceDir: workspace,
		Provider:         prov,
	}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !storeRes.OK {
		t.Fatalf("blackboard_store failed: %s", storeRes.Output)
	}

	searchArgs, err := json.Marshal(map[string]any{
		"query": "replay artifact",
		"limit": 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	searchRes, err := BlackboardSearch().Exec(context.Background(), ToolCallContext{
		RunID:            "run-2",
		WorkspaceDir:     t.TempDir(),
		HostWorkspaceDir: workspace,
		Provider:         prov,
	}, searchArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !searchRes.OK {
		t.Fatalf("blackboard_search failed: %s", searchRes.Output)
	}
	if !strings.Contains(searchRes.Output, "remember the replay artifact convention") {
		t.Fatalf("blackboard_search output = %q, want shared workspace vector memory", searchRes.Output)
	}

	if _, err := os.Stat(filepath.Join(workspace, "blackboard.vectors.json")); err != nil {
		t.Fatalf("expected workspace vector store file: %v", err)
	}
}
