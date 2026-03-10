package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseGitignoreExcludes(t *testing.T) {
	in := `
# comments ignored
runs/
.gocache/
node_modules
dist/*.map
!keep.me
`
	got := parseGitignoreExcludes(in)
	contains := func(want string) bool {
		for _, v := range got {
			if v == want {
				return true
			}
		}
		return false
	}
	wants := []string{
		"runs/**",
		"**/runs/**",
		".gocache/**",
		"**/.gocache/**",
		"node_modules",
		"**/node_modules",
		"**/node_modules/**",
		"dist/*.map",
	}
	for _, w := range wants {
		if !contains(w) {
			t.Fatalf("expected parsed excludes to contain %q; got=%v", w, got)
		}
	}
	if contains("keep.me") {
		t.Fatalf("negated pattern should not be included")
	}
}

func newSearchCall(t *testing.T, dir string) ToolCallContext {
	t.Helper()
	return ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &MockMapper{Dir: dir},
	}
}

func marshalSearchArgs(t *testing.T, pattern, glob string) json.RawMessage {
	t.Helper()
	m := map[string]any{"pattern": pattern}
	if glob != "" {
		m["glob"] = glob
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestProjectSearchRealNoMatch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not in PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.go", []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ProjectSearch()
	result, err := tool.Exec(context.Background(), newSearchCall(t, dir), marshalSearchArgs(t, "THIS_PATTERN_WILL_NEVER_MATCH_XYZ", ""))
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("real no-match should still return OK=true, got output: %s", result.Output)
	}
	if !strings.Contains(result.Output, "(no matches)") {
		t.Fatalf("expected '(no matches)', got: %s", result.Output)
	}
}

func TestProjectSearchSuccessfulMatch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not in PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/hello.go", []byte("package main\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ProjectSearch()
	result, err := tool.Exec(context.Background(), newSearchCall(t, dir), marshalSearchArgs(t, "func Hello", ""))
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatalf("successful match should return OK=true, got output: %s", result.Output)
	}
	if strings.Contains(result.Output, "(no matches)") {
		t.Fatalf("expected match output, got '(no matches)'")
	}
	if !strings.Contains(result.Output, "Hello") {
		t.Fatalf("expected 'Hello' in output, got: %s", result.Output)
	}
}

func TestProjectSearchCommandFailureNotSilenced(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not in PATH")
	}
	dir := t.TempDir()
	tool := ProjectSearch()
	// An invalid regex causes rg to exit with code 2 (execution error), not 1 (no match).
	result, err := tool.Exec(context.Background(), newSearchCall(t, dir), marshalSearchArgs(t, "[invalid regex(", ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.OK {
		t.Fatalf("rg execution error should return OK=false, got output: %s", result.Output)
	}
	if strings.Contains(result.Output, "(no matches)") {
		t.Fatalf("rg execution error must not be reported as '(no matches)', got: %s", result.Output)
	}
}
