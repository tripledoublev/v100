package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

type stubResearchProvider struct {
	responses []string
	prompts   []string
}

func (p *stubResearchProvider) Name() string { return "stub" }
func (p *stubResearchProvider) Capabilities() providers.Capabilities {
	return providers.Capabilities{}
}
func (p *stubResearchProvider) Complete(_ context.Context, req providers.CompleteRequest) (providers.CompleteResponse, error) {
	if len(req.Messages) > 0 {
		p.prompts = append(p.prompts, req.Messages[len(req.Messages)-1].Content)
	}
	text := "stub response"
	if len(p.responses) > 0 {
		text = p.responses[0]
		p.responses = p.responses[1:]
	}
	return providers.CompleteResponse{AssistantText: text}, nil
}
func (p *stubResearchProvider) Embed(context.Context, providers.EmbedRequest) (providers.EmbedResponse, error) {
	return providers.EmbedResponse{}, nil
}
func (p *stubResearchProvider) Metadata(context.Context, string) (providers.ModelMetadata, error) {
	return providers.ModelMetadata{}, nil
}

func setupDeepResearchHTTP(t *testing.T) (*httptest.Server, *httptest.Server) {
	t.Helper()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Evidence Page</title></head><body><h1>Evidence Page</h1><p>Alpha evidence supports the main claim with concrete public data.</p></body></html>`))
	}))
	t.Cleanup(source.Close)

	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Subscription-Token"); got != "test-key" {
			t.Fatalf("missing Brave token header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{
				"results": []map[string]string{
					{
						"title":       "Evidence Page",
						"url":         source.URL + "/evidence",
						"description": "Public evidence summary.",
					},
				},
			},
		})
	}))
	t.Cleanup(search.Close)
	return search, source
}

func TestDeepResearchFetchesSourcesAndSynthesizesCitedReport(t *testing.T) {
	search, _ := setupDeepResearchHTTP(t)
	oldEndpoint := braveSearchEndpoint
	braveSearchEndpoint = search.URL
	t.Cleanup(func() { braveSearchEndpoint = oldEndpoint })
	t.Setenv("BRAVE_SEARCH_API_KEY", "test-key")

	prov := &stubResearchProvider{responses: []string{
		"## Answer\nThe main claim is supported by public evidence [S1].\n\n## Evidence\n- Alpha evidence [S1].\n\n## Uncertainties And Conflicts\n- None found.\n\n## Sources\n- [S1] Evidence Page",
		"## Verification\n- The cited claim is supported by [S1].\n\n## Corrections\n- No correction required.",
	}}
	args, err := json.Marshal(map[string]any{
		"topic":        "alpha claim",
		"queries":      []string{"alpha claim evidence"},
		"max_sources":  1,
		"perspectives": false,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewDeepResearch(nil).Exec(context.Background(), ToolCallContext{
		RunID:    "run-1",
		StepID:   "step-1",
		Provider: prov,
	}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("deep_research failed: %s", res.Output)
	}
	for _, want := range []string{
		"# Deep Research Report",
		"The main claim is supported by public evidence [S1]",
		"# Adversarial Verification",
		"[S1] Evidence Page",
	} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Output)
		}
	}
	if len(prov.prompts) != 2 {
		t.Fatalf("provider prompts = %d, want synthesis + verification", len(prov.prompts))
	}
	if !strings.Contains(prov.prompts[0], "Source pack") || !strings.Contains(prov.prompts[1], "Report to audit") {
		t.Fatalf("provider prompts did not include expected research stages: %#v", prov.prompts)
	}
}

func TestDeepResearchRunsPerspectiveSubAgentsWithProviderOverrides(t *testing.T) {
	search, _ := setupDeepResearchHTTP(t)
	oldEndpoint := braveSearchEndpoint
	braveSearchEndpoint = search.URL
	t.Cleanup(func() { braveSearchEndpoint = oldEndpoint })
	t.Setenv("BRAVE_SEARCH_API_KEY", "test-key")

	var calls []AgentRunParams
	runFn := func(_ context.Context, params AgentRunParams) AgentRunResult {
		calls = append(calls, params)
		return AgentRunResult{OK: true, Result: "## Summary\nPerspective.\n\n## Findings\n- Evidence [S1].\n\n## Next Steps\n1. Synthesize."}
	}
	args, err := json.Marshal(map[string]any{
		"topic":                 "model plurality",
		"queries":               []string{"model plurality evidence"},
		"max_sources":           1,
		"primary_providers":     []string{"glm", "minimax:MiniMax-M2.7"},
		"review_providers":      []string{"gemini:gemini-2.5-pro", "codex"},
		"max_perspective_steps": 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := NewDeepResearch(runFn).Exec(context.Background(), ToolCallContext{
		RunID:        "run-1",
		StepID:       "step-1",
		CallID:       "call-1",
		WorkspaceDir: t.TempDir(),
		StateDir:     t.TempDir(),
	}, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("deep_research failed: %s", res.Output)
	}
	if len(calls) != 4 {
		t.Fatalf("perspective calls = %d, want 4", len(calls))
	}
	wants := []struct {
		provider string
		model    string
	}{
		{provider: "glm"},
		{provider: "minimax", model: "MiniMax-M2.7"},
		{provider: "gemini", model: "gemini-2.5-pro"},
		{provider: "codex"},
	}
	for i, want := range wants {
		if calls[i].Provider != want.provider || calls[i].Model != want.model {
			t.Fatalf("call %d provider/model = %q/%q, want %q/%q", i, calls[i].Provider, calls[i].Model, want.provider, want.model)
		}
		if calls[i].MaxSteps != 3 {
			t.Fatalf("call %d max steps = %d, want 3", i, calls[i].MaxSteps)
		}
		if strings.Join(calls[i].Tools, ",") != "web_search,web_extract" {
			t.Fatalf("call %d tools = %v", i, calls[i].Tools)
		}
		if !strings.Contains(calls[i].Task, "[S1]") {
			t.Fatalf("call %d task missing source pack: %s", i, calls[i].Task)
		}
	}
	if !strings.Contains(res.Output, "# Perspective Notes") {
		t.Fatalf("output missing perspective notes:\n%s", res.Output)
	}
}
