package tools

import (
	"strings"
	"testing"
)

func TestSecretRedactorRedactsToolAndPatternEnvValues(t *testing.T) {
	t.Setenv("HIDDEN_TOKEN", "parent-secret-value")
	redactor := NewSecretRedactor([]string{"*_TOKEN"}, []string{"GH_TOKEN=tool-secret-value"})

	got := redactor.RedactText("a=tool-secret-value b=parent-secret-value")
	if strings.Contains(got, "tool-secret-value") || strings.Contains(got, "parent-secret-value") {
		t.Fatalf("secret leaked after redaction: %q", got)
	}
	if strings.Count(got, redactedSecret) != 2 {
		t.Fatalf("expected two redactions, got %q", got)
	}
}

func TestEnvNamesSortsValidEnvEntries(t *testing.T) {
	got := strings.Join(EnvNames([]string{"ZED=1", "bad-name=2", "A=3"}), ",")
	if got != "A,ZED" {
		t.Fatalf("EnvNames() = %q, want A,ZED", got)
	}
}

func TestGitHubCLIDiagnosticDistinguishesMissingAndRejectedToken(t *testing.T) {
	missing := GitHubCLIDiagnostic("To get started with GitHub CLI, please run: gh auth login", nil)
	if !strings.Contains(missing, "GitHub CLI auth unavailable") || !strings.Contains(missing, "[tools.auth.github]") {
		t.Fatalf("unexpected missing-token diagnostic: %q", missing)
	}

	rejected := GitHubCLIDiagnostic("HTTP 401: Bad credentials", []string{"GH_TOKEN"})
	if !strings.Contains(rejected, "configured GH_TOKEN/GITHUB_TOKEN was rejected") {
		t.Fatalf("unexpected rejected-token diagnostic: %q", rejected)
	}

	forbidden := GitHubCLIDiagnostic("GraphQL: Resource not accessible by integration", []string{"GH_TOKEN"})
	if !strings.Contains(forbidden, "lacks permission") {
		t.Fatalf("unexpected permission diagnostic: %q", forbidden)
	}
}
