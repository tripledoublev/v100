package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core/executor"
)

// ---------------------------------------------------------------------------
// Wiki tool tests
// ---------------------------------------------------------------------------

type fakeWikiSession struct {
	result  executor.Result
	lastReq executor.RunRequest
}

func (s *fakeWikiSession) ID() string                  { return "wiki-test" }
func (s *fakeWikiSession) Type() string                { return "docker" }
func (s *fakeWikiSession) Start(context.Context) error { return nil }
func (s *fakeWikiSession) Close() error                { return nil }
func (s *fakeWikiSession) Workspace() string           { return "/tmp/workspace" }
func (s *fakeWikiSession) Run(_ context.Context, req executor.RunRequest) (executor.Result, error) {
	s.lastReq = req
	return s.result, nil
}

const wikiQuebecJSON = `{
	"batchcomplete": "",
	"query": {
		"pages": {
			"7954867": {
				"pageid": 7954867,
				"ns": 0,
				"title": "Quebec",
				"extract": "Quebec is one of the thirteen provinces and territories of Canada. It is the largest province by area and the second-largest by population."
			}
		}
	}
}`

const wikiSearchJSON = `{
	"query": {
		"search": [
			{"title": "Quebec", "snippet": "Quebec is a <span class=\"searchmatch\">province</span> of Canada."},
			{"title": "Quebec City", "snippet": "Quebec City is the <span class=\"searchmatch\">capital</span> of Quebec."}
		]
	}
}`

func TestWikiReadFromRemote(t *testing.T) {
	session := &fakeWikiSession{
		result: executor.Result{ExitCode: 0, Stdout: wikiQuebecJSON},
	}

	tmpDir := t.TempDir()
	call := ToolCallContext{
		Session:          session,
		WorkspaceDir:     tmpDir,
		HostWorkspaceDir: tmpDir,
	}
	args, _ := json.Marshal(map[string]any{
		"action": "read",
		"title":  "Quebec",
		"lang":   "en",
	})

	res, err := Wiki().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("wiki read failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "source: remote") {
		t.Fatalf("expected source: remote, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Quebec is one of the thirteen") {
		t.Fatalf("expected article extract, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "title: Quebec") {
		t.Fatalf("expected title, got:\n%s", res.Output)
	}
}

func TestWikiReadFromCache(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, ".v100", "wiki", "en")

	_ = os.MkdirAll(cacheDir, 0o755)
	article := &wikiArticle{
		Title:     "Quebec",
		PageID:    7954867,
		Extract:   "Cached extract about Quebec.",
		Lang:      "en",
		FetchedAt: parseTime(t, "2025-01-01T00:00:00Z"),
	}
	_ = wikiWriteCache(cacheDir, "quebec", article)

	session := &fakeWikiSession{
		result: executor.Result{ExitCode: 0, Stdout: wikiQuebecJSON},
	}
	call := ToolCallContext{
		Session:          session,
		WorkspaceDir:     tmpDir,
		HostWorkspaceDir: tmpDir,
	}
	args, _ := json.Marshal(map[string]any{
		"action": "read",
		"title":  "Quebec",
		"lang":   "en",
	})

	res, err := Wiki().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("wiki read failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "source: cache") {
		t.Fatalf("expected source: cache, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Cached extract about Quebec") {
		t.Fatalf("expected cached extract, got:\n%s", res.Output)
	}
}

func TestWikiUpdateForcesRefetch(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := filepath.Join(tmpDir, ".v100", "wiki", "en")

	_ = os.MkdirAll(cacheDir, 0o755)
	article := &wikiArticle{
		Title:     "Quebec",
		PageID:    7954867,
		Extract:   "Old cached content.",
		Lang:      "en",
		FetchedAt: parseTime(t, "2024-01-01T00:00:00Z"),
	}
	_ = wikiWriteCache(cacheDir, "quebec", article)

	session := &fakeWikiSession{
		result: executor.Result{ExitCode: 0, Stdout: wikiQuebecJSON},
	}
	call := ToolCallContext{
		Session:          session,
		WorkspaceDir:     tmpDir,
		HostWorkspaceDir: tmpDir,
	}
	args, _ := json.Marshal(map[string]any{
		"action": "update",
		"title":  "Quebec",
		"lang":   "en",
	})

	res, err := Wiki().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("wiki update failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "source: remote") {
		t.Fatalf("expected source: remote on update, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Quebec is one of the thirteen") {
		t.Fatalf("expected fresh extract, got:\n%s", res.Output)
	}
}

func TestWikiSearch(t *testing.T) {
	session := &fakeWikiSession{
		result: executor.Result{ExitCode: 0, Stdout: wikiSearchJSON},
	}

	tmpDir := t.TempDir()
	call := ToolCallContext{
		Session:          session,
		WorkspaceDir:     tmpDir,
		HostWorkspaceDir: tmpDir,
	}
	args, _ := json.Marshal(map[string]any{
		"action":      "search",
		"query":       "Quebec",
		"lang":        "en",
		"max_results": 5,
	})

	res, err := Wiki().Exec(context.Background(), call, args)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("wiki search failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Quebec —") {
		t.Fatalf("expected search results, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "Quebec City —") {
		t.Fatalf("expected Quebec City result, got:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "<span") {
		t.Fatalf("HTML should be stripped from snippets, got:\n%s", res.Output)
	}
}

func TestWikiTruncateSentences(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence. Fourth one."
	got := wikiTruncateSentences(text, 2)
	if got != "First sentence. Second sentence. " {
		t.Fatalf("unexpected truncation: %q", got)
	}
}

func TestWikiSlug(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Quebec", "quebec"},
		{"New York City", "new_york_city"},
		{"Montreal, QC", "montreal_qc"},
		{"", ""},
	}
	for _, tt := range tests {
		got := wikiSlug(tt.input)
		if got != tt.want {
			t.Errorf("wikiSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWikiStripHTML(t *testing.T) {
	got := wikiStripHTML(`<span class="searchmatch">Quebec</span> is a province`)
	if got != "Quebec is a province" {
		t.Fatalf("unexpected: %q", got)
	}
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
