package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/tools"
)

func TestSemTools(t *testing.T) {
	if !hasSemanticSem(t) {
		t.Skip("semantic sem tool not found in PATH, skipping integration tests")
	}

	ctx := context.Background()
	dir := t.TempDir()
	call := tools.ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       &tools.MockMapper{Dir: dir},
	}

	// Initialize git repo in temp dir
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v failed: %v", args, err)
		}
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")

	// Create a file and commit it
	content1 := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(dir+"/main.go", []byte(content1), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "initial commit")

	// Modify the file
	content2 := "package main\n\nfunc main() {\n\t// added comment\n}\n"
	if err := os.WriteFile(dir+"/main.go", []byte(content2), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("SemDiff", func(t *testing.T) {
		tool := tools.SemDiff()
		args, _ := json.Marshal(map[string]bool{"staged": false})
		res, err := tool.Exec(ctx, call, args)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Fatalf("sem_diff failed: %s", res.Output)
		}

		// Verify JSON output structure
		var out struct {
			Changes []interface{} `json:"changes"`
		}
		if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
			t.Fatalf("failed to parse sem_diff output: %v\nOutput: %s", err, res.Output)
		}
		if len(out.Changes) == 0 {
			t.Error("expected at least one change in sem_diff output")
		}
	})

	t.Run("SemBlame", func(t *testing.T) {
		tool := tools.SemBlame()
		args, _ := json.Marshal(map[string]string{"path": "main.go"})
		res, err := tool.Exec(ctx, call, args)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Fatalf("sem_blame failed: %s", res.Output)
		}
		if res.Output == "" {
			t.Error("sem_blame output is empty")
		}
	})

	t.Run("SemImpact", func(t *testing.T) {
		// impact might return nothing if no dependencies, but it should still run OK
		tool := tools.SemImpact()
		args, _ := json.Marshal(map[string]string{"entity": "main"})
		res, err := tool.Exec(ctx, call, args)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Fatalf("sem_impact failed: %s", res.Output)
		}
	})
}

func TestSemGracefulFailure(t *testing.T) {
	// Mock PATH to NOT include sem
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/usr/bin:/bin") // Assume sem is not in /usr/bin or /bin for this test

	// If sem is in /usr/bin or /bin, this might still find it.
	// But usually it's in /usr/local/bin or ~/.cargo/bin.
	// To be safer, we could set PATH to an empty string, but then git/other things might fail
	// if the tool calls them. However, SemDiff().Exec calls exec.LookPath("sem") before doing anything.

	if _, err := exec.LookPath("sem"); err == nil {
		t.Skip("sem still found in restricted PATH, skipping graceful failure test")
	}

	tool := tools.SemDiff()
	ctx := context.Background()
	call := tools.ToolCallContext{}

	res, err := tool.Exec(ctx, call, json.RawMessage("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Error("expected failure when sem is missing")
	}
	if !strings.Contains(res.Output, "not installed") {
		t.Errorf("expected 'not installed' error message, got: %s", res.Output)
	}
}

func hasSemanticSem(t *testing.T) bool {
	t.Helper()

	if _, err := exec.LookPath("sem"); err != nil {
		return false
	}
	out, err := exec.Command("sem", "--help").CombinedOutput()
	if err != nil {
		return false
	}
	help := string(out)
	return strings.Contains(help, "Semantic version control") &&
		strings.Contains(help, "Show semantic diff of changes")
}
