package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/providers"
)

type deepResearchTool struct {
	runFn AgentRunFn
}

// NewDeepResearch creates a web research harness that fans out Brave searches,
// fetches sources, asks model/sub-agent perspectives, and returns a cited report.
func NewDeepResearch(runFn AgentRunFn) Tool {
	return &deepResearchTool{runFn: runFn}
}

func (t *deepResearchTool) Name() string { return "deep_research" }
func (t *deepResearchTool) Description() string {
	return "Run a deep web research harness: fan out Brave searches, fetch source extracts, ask adversarial model perspectives, and synthesize a cited report."
}
func (t *deepResearchTool) DangerLevel() DangerLevel { return Safe }
func (t *deepResearchTool) Effects() ToolEffects {
	return ToolEffects{
		MutatesRunState:    true,
		NeedsNetwork:       true,
		ExternalSideEffect: true,
	}
}

func (t *deepResearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"required": ["topic"],
		"properties": {
			"topic": {"type": "string", "description": "Research question or topic."},
			"queries": {"type": "array", "items": {"type": "string"}, "description": "Optional explicit Brave search queries. If omitted, queries are derived from topic."},
			"max_queries": {"type": "integer", "description": "Maximum search queries to run (1-8, default 4).", "default": 4},
			"results_per_query": {"type": "integer", "description": "Brave results per query (1-10, default 5).", "default": 5},
			"max_sources": {"type": "integer", "description": "Maximum unique sources to fetch (1-20, default 8).", "default": 8},
			"max_bytes": {"type": "integer", "description": "Maximum bytes to fetch per source.", "default": %d},
			"perspectives": {"type": "boolean", "description": "When true, run model/provider perspective sub-agents before synthesis.", "default": true},
			"primary_providers": {"type": "array", "items": {"type": "string"}, "description": "Provider or provider:model specs for primary perspectives, e.g. glm, minimax:MiniMax-M2.7.", "default": ["glm", "minimax"]},
			"review_providers": {"type": "array", "items": {"type": "string"}, "description": "Provider or provider:model specs for adversarial review perspectives.", "default": ["gemini", "codex"]},
			"max_perspective_steps": {"type": "integer", "description": "Step cap for each perspective sub-agent (default 4).", "default": 4}
		}
	}`, DefaultFetchBytes))
}

func (t *deepResearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"report": {"type": "string"},
			"verification": {"type": "string"},
			"sources": {"type": "array", "items": {"type": "object"}},
			"perspectives": {"type": "array", "items": {"type": "object"}}
		}
	}`)
}

type deepResearchArgs struct {
	Topic               string   `json:"topic"`
	Queries             []string `json:"queries"`
	MaxQueries          int      `json:"max_queries"`
	ResultsPerQuery     int      `json:"results_per_query"`
	MaxSources          int      `json:"max_sources"`
	MaxBytes            int64    `json:"max_bytes"`
	Perspectives        *bool    `json:"perspectives"`
	PrimaryProviders    []string `json:"primary_providers"`
	ReviewProviders     []string `json:"review_providers"`
	MaxPerspectiveSteps int      `json:"max_perspective_steps"`
}

type deepResearchSource struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Extract     string `json:"extract,omitempty"`
	Error       string `json:"error,omitempty"`
}

type deepResearchPerspective struct {
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Role     string `json:"role"`
	OK       bool   `json:"ok"`
	Result   string `json:"result"`
}

func (t *deepResearchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a deepResearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	a.Topic = strings.TrimSpace(a.Topic)
	if a.Topic == "" {
		return failResult(start, "topic is required"), nil
	}
	normalizeDeepResearchArgs(&a)

	apiKey := os.Getenv("BRAVE_SEARCH_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		return failResult(start, "BRAVE_SEARCH_API_KEY environment variable is not set"), nil
	}

	queries := normalizeDeepResearchQueries(a.Topic, a.Queries, a.MaxQueries)
	searchResults, searchNotes := runDeepResearchSearches(ctx, call, queries, a.ResultsPerQuery, apiKey)
	sources := fetchDeepResearchSources(ctx, call, searchResults, a.MaxSources, a.MaxBytes)
	if len(sources) == 0 {
		if len(searchNotes) == 0 {
			searchNotes = append(searchNotes, "no unique Brave results returned")
		}
		return failResult(start, "no sources fetched: "+strings.Join(searchNotes, "; ")), nil
	}

	sourcePack := formatDeepResearchSourcePack(sources, 2600)
	perspectives := t.runPerspectives(ctx, call, a, sourcePack)
	report, reportErr := synthesizeDeepResearchReport(ctx, call, a.Topic, sourcePack, perspectives)
	verification, verifyErr := verifyDeepResearchReport(ctx, call, a.Topic, sourcePack, report, perspectives)
	output := formatDeepResearchOutput(a.Topic, queries, sources, perspectives, report, verification, appendModelErrors(searchNotes, reportErr, verifyErr))

	return ToolResult{
		OK:         true,
		Output:     output,
		Stdout:     output,
		TaintLevel: "external_data",
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func normalizeDeepResearchArgs(a *deepResearchArgs) {
	if a.MaxQueries <= 0 {
		a.MaxQueries = 4
	}
	a.MaxQueries = clampInt(a.MaxQueries, 1, 8)
	if a.ResultsPerQuery <= 0 {
		a.ResultsPerQuery = 5
	}
	a.ResultsPerQuery = clampInt(a.ResultsPerQuery, 1, 10)
	if a.MaxSources <= 0 {
		a.MaxSources = 8
	}
	a.MaxSources = clampInt(a.MaxSources, 1, 20)
	if a.MaxBytes <= 0 || a.MaxBytes > MaxFetchBytes {
		a.MaxBytes = DefaultFetchBytes
	}
	if len(a.PrimaryProviders) == 0 {
		a.PrimaryProviders = []string{"glm", "minimax"}
	}
	if len(a.ReviewProviders) == 0 {
		a.ReviewProviders = []string{"gemini", "codex"}
	}
	if a.MaxPerspectiveSteps <= 0 {
		a.MaxPerspectiveSteps = 4
	}
	a.MaxPerspectiveSteps = clampInt(a.MaxPerspectiveSteps, 1, 12)
}

func normalizeDeepResearchQueries(topic string, explicit []string, maxQueries int) []string {
	candidates := make([]string, 0, maxQueries)
	candidates = append(candidates, explicit...)
	if len(candidates) == 0 {
		candidates = append(candidates,
			topic,
			topic+" evidence",
			topic+" criticism limitations",
			topic+" latest report data",
		)
	}
	return uniqueNonEmpty(candidates, maxQueries)
}

func runDeepResearchSearches(ctx context.Context, call ToolCallContext, queries []string, resultsPerQuery int, apiKey string) ([]braveResult, []string) {
	var out []braveResult
	var notes []string
	for _, query := range queries {
		results, err := braveSearch(ctx, call, query, resultsPerQuery, apiKey)
		if err != nil {
			notes = append(notes, fmt.Sprintf("search %q failed: %v", query, err))
			continue
		}
		out = append(out, results...)
	}
	return out, notes
}

func fetchDeepResearchSources(ctx context.Context, call ToolCallContext, results []braveResult, maxSources int, maxBytes int64) []deepResearchSource {
	seen := make(map[string]bool)
	sources := make([]deepResearchSource, 0, maxSources)
	for _, r := range results {
		rawURL := strings.TrimSpace(r.URL)
		if rawURL == "" {
			continue
		}
		key := deepResearchURLKey(rawURL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		source := deepResearchSource{
			ID:      len(sources) + 1,
			Title:   strings.TrimSpace(r.Title),
			URL:     rawURL,
			Snippet: strings.TrimSpace(r.Description),
		}
		fetched, fail, err := fetchHTTPBody(ctx, call, time.Now(), rawURL, maxBytes, false)
		switch {
		case err != nil:
			source.Error = err.Error()
		case fail != nil:
			source.Error = fail.Output
		default:
			source.Status = fetched.status
			source.ContentType = fetched.contentType
			source.Extract = strings.TrimSpace(fetched.text)
		}
		sources = append(sources, source)
		if len(sources) >= maxSources {
			break
		}
	}
	return sources
}

func (t *deepResearchTool) runPerspectives(ctx context.Context, call ToolCallContext, a deepResearchArgs, sourcePack string) []deepResearchPerspective {
	if a.Perspectives != nil && !*a.Perspectives {
		return nil
	}
	if t.runFn == nil {
		return nil
	}

	specs := make([]struct {
		role string
		spec string
	}, 0, len(a.PrimaryProviders)+len(a.ReviewProviders))
	for _, spec := range a.PrimaryProviders {
		specs = append(specs, struct {
			role string
			spec string
		}{role: "primary", spec: spec})
	}
	for _, spec := range a.ReviewProviders {
		specs = append(specs, struct {
			role string
			spec string
		}{role: "adversarial", spec: spec})
	}

	callIDBase := strings.TrimSpace(call.CallID)
	if callIDBase == "" {
		callIDBase = "deep_research"
	}

	out := make([]deepResearchPerspective, 0, len(specs))
	for i, item := range specs {
		providerName, model := parseProviderModelSpec(item.spec)
		if providerName == "" {
			continue
		}
		task := buildDeepResearchPerspectiveTask(a.Topic, item.role, sourcePack)
		res := t.runFn(ctx, AgentRunParams{
			CallID:       fmt.Sprintf("%s-perspective-%d", callIDBase, i+1),
			RunID:        call.RunID,
			StepID:       call.StepID,
			Pattern:      "fanout",
			Task:         task,
			Provider:     providerName,
			Model:        model,
			Tools:        []string{"web_search", "web_extract"},
			MaxSteps:     a.MaxPerspectiveSteps,
			WorkspaceDir: call.WorkspaceDir,
			StateDir:     call.StateDir,
		})
		result := strings.TrimSpace(res.Result)
		if result == "" {
			result = "(no output)"
		}
		out = append(out, deepResearchPerspective{
			Provider: providerName,
			Model:    model,
			Role:     item.role,
			OK:       res.OK,
			Result:   result,
		})
	}
	return out
}

func buildDeepResearchPerspectiveTask(topic, role, sourcePack string) string {
	instruction := "Extract the strongest evidence-backed answer and list the most important uncertainties."
	if role == "adversarial" {
		instruction = "Adversarially audit the evidence. Identify unsupported claims, conflicts, missing source types, and what would change your mind."
	}
	return fmt.Sprintf(`You are one model perspective inside a deep research harness.

Topic:
%s

Role:
%s

Instructions:
%s

Use the source pack below as primary evidence. You may call web_search or web_extract only if the pack is insufficient. Cite source IDs like [S1] and any extra URLs you fetch.

Source pack:
%s`, topic, role, instruction, sourcePack)
}

func synthesizeDeepResearchReport(ctx context.Context, call ToolCallContext, topic, sourcePack string, perspectives []deepResearchPerspective) (string, error) {
	if call.Provider == nil {
		return fallbackDeepResearchReport(topic, sourcePack, perspectives), nil
	}
	prompt := fmt.Sprintf(`You are v100 Deep Research. Synthesize a rigorous cited report.

Question:
%s

Rules:
- Use only the source pack and perspective notes below.
- Cite every factual claim with source IDs like [S1].
- Separate what is well-supported from what is uncertain.
- Prefer concrete evidence over generalities.
- If sources conflict or are weak, say so directly.

Return Markdown with:
## Answer
## Evidence
## Uncertainties And Conflicts
## Sources

Source pack:
%s

Perspective notes:
%s`, topic, sourcePack, formatPerspectiveNotes(perspectives, 1800))

	resp, err := call.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    call.RunID,
		StepID:   call.StepID,
		Messages: []providers.Message{{Role: "user", Content: prompt}},
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.1),
			MaxTokens:   3000,
		},
	})
	if err != nil {
		return fallbackDeepResearchReport(topic, sourcePack, perspectives), err
	}
	text := strings.TrimSpace(resp.AssistantText)
	if text == "" {
		return fallbackDeepResearchReport(topic, sourcePack, perspectives), fmt.Errorf("empty synthesis response")
	}
	return text, nil
}

func verifyDeepResearchReport(ctx context.Context, call ToolCallContext, topic, sourcePack, report string, perspectives []deepResearchPerspective) (string, error) {
	if call.Provider == nil {
		return "No model verifier was available. Treat this as an unverified source digest.", nil
	}
	prompt := fmt.Sprintf(`You are the adversarial verifier for a deep research report.

Question:
%s

Check whether the report below is supported by the source pack and perspective notes.

Return Markdown with:
## Verification
- Supported claims with source IDs.
- Weak, unsupported, stale, or overbroad claims.
- Conflicts between sources.

## Corrections
- Concrete edits or cautions needed before relying on the report.

Source pack:
%s

Perspective notes:
%s

Report to audit:
%s`, topic, sourcePack, formatPerspectiveNotes(perspectives, 1400), report)

	resp, err := call.Provider.Complete(ctx, providers.CompleteRequest{
		RunID:    call.RunID,
		StepID:   call.StepID,
		Messages: []providers.Message{{Role: "user", Content: prompt}},
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.0),
			MaxTokens:   1800,
		},
	})
	if err != nil {
		return "Adversarial verification failed: " + err.Error(), err
	}
	text := strings.TrimSpace(resp.AssistantText)
	if text == "" {
		return "Adversarial verification returned no text.", fmt.Errorf("empty verification response")
	}
	return text, nil
}

func formatDeepResearchOutput(topic string, queries []string, sources []deepResearchSource, perspectives []deepResearchPerspective, report, verification string, notes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "topic: %s\n", topic)
	fmt.Fprintf(&b, "queries: %s\n", strings.Join(queries, " | "))
	fmt.Fprintf(&b, "sources: %d\n", len(sources))
	if len(perspectives) > 0 {
		fmt.Fprintf(&b, "perspectives: %d\n", len(perspectives))
	}
	if len(notes) > 0 {
		b.WriteString("notes:\n")
		for _, note := range notes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	b.WriteString("\n# Deep Research Report\n\n")
	b.WriteString(strings.TrimSpace(report))
	b.WriteString("\n\n# Adversarial Verification\n\n")
	b.WriteString(strings.TrimSpace(verification))
	b.WriteString("\n\n# Source Register\n\n")
	for _, s := range sources {
		fmt.Fprintf(&b, "[S%d] %s\nURL: %s\n", s.ID, emptyDefault(s.Title, "(untitled)"), s.URL)
		if s.Snippet != "" {
			fmt.Fprintf(&b, "Snippet: %s\n", compactWhitespace(s.Snippet))
		}
		if s.Error != "" {
			fmt.Fprintf(&b, "Fetch: ERROR: %s\n", s.Error)
		} else {
			fmt.Fprintf(&b, "Fetch: status=%d content_type=%s\n", s.Status, emptyDefault(s.ContentType, "unknown"))
		}
		b.WriteString("\n")
	}
	if len(perspectives) > 0 {
		b.WriteString("# Perspective Notes\n\n")
		for _, p := range perspectives {
			label := p.Provider
			if p.Model != "" {
				label += ":" + p.Model
			}
			status := "ok"
			if !p.OK {
				status = "failed"
			}
			fmt.Fprintf(&b, "## %s (%s, %s)\n%s\n\n", label, p.Role, status, strings.TrimSpace(p.Result))
		}
	}
	return b.String()
}

func formatDeepResearchSourcePack(sources []deepResearchSource, extractLimit int) string {
	var b strings.Builder
	for _, s := range sources {
		fmt.Fprintf(&b, "[S%d] %s\nURL: %s\n", s.ID, emptyDefault(s.Title, "(untitled)"), s.URL)
		if s.Snippet != "" {
			fmt.Fprintf(&b, "Search snippet: %s\n", compactWhitespace(s.Snippet))
		}
		if s.Error != "" {
			fmt.Fprintf(&b, "Fetch error: %s\n\n", s.Error)
			continue
		}
		extract := strings.TrimSpace(s.Extract)
		if extractLimit > 0 && len(extract) > extractLimit {
			extract = extract[:extractLimit] + "\n[truncated]"
		}
		fmt.Fprintf(&b, "Extract:\n%s\n\n", extract)
	}
	return strings.TrimSpace(b.String())
}

func formatPerspectiveNotes(perspectives []deepResearchPerspective, limitEach int) string {
	if len(perspectives) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, p := range perspectives {
		label := p.Provider
		if p.Model != "" {
			label += ":" + p.Model
		}
		text := strings.TrimSpace(p.Result)
		if limitEach > 0 && len(text) > limitEach {
			text = text[:limitEach] + "\n[truncated]"
		}
		fmt.Fprintf(&b, "- %s (%s, ok=%v):\n%s\n\n", label, p.Role, p.OK, text)
	}
	return strings.TrimSpace(b.String())
}

func fallbackDeepResearchReport(topic, sourcePack string, perspectives []deepResearchPerspective) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Answer\nA model synthesis was unavailable, so this is a source digest for: %s.\n\n", topic)
	b.WriteString("## Evidence\n")
	for _, line := range strings.Split(sourcePack, "\n") {
		if strings.HasPrefix(line, "[S") || strings.HasPrefix(line, "URL:") || strings.HasPrefix(line, "Search snippet:") {
			b.WriteString("- " + line + "\n")
		}
	}
	if len(perspectives) > 0 {
		b.WriteString("\n## Model Perspectives\n")
		for _, p := range perspectives {
			fmt.Fprintf(&b, "- %s/%s ok=%v: %s\n", p.Provider, p.Role, p.OK, truncateOneLine(p.Result, 260))
		}
	}
	b.WriteString("\n## Uncertainties And Conflicts\n- No synthesis model completed the claim-level analysis.\n\n## Sources\n- See the Source Register below.\n")
	return b.String()
}

func appendModelErrors(notes []string, errs ...error) []string {
	for _, err := range errs {
		if err != nil {
			notes = append(notes, err.Error())
		}
	}
	return notes
}

func parseProviderModelSpec(spec string) (string, string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	providerName, model, ok := strings.Cut(spec, ":")
	if !ok {
		return strings.TrimSpace(spec), ""
	}
	return strings.TrimSpace(providerName), strings.TrimSpace(model)
}

func deepResearchURLKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(strings.ToLower(raw))
	}
	u.Fragment = ""
	u.RawQuery = strings.TrimRight(u.RawQuery, "&")
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func uniqueNonEmpty(values []string, limit int) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, min(limit, len(values)))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func compactWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func emptyDefault(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}

func truncateOneLine(s string, maxChars int) string {
	s = compactWhitespace(s)
	if maxChars > 0 && len(s) > maxChars {
		return s[:maxChars] + "..."
	}
	return s
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var _ Tool = (*deepResearchTool)(nil)
