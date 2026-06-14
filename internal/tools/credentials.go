package tools

import (
	"os"
	"path"
	"sort"
	"strings"
	"unicode"
)

const redactedSecret = "[REDACTED]"

// EnvNames returns the variable names from KEY=value env entries.
func EnvNames(env []string) []string {
	names := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := splitEnvEntry(entry)
		if ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// SecretRedactor redacts configured secret values from tool-visible text.
type SecretRedactor struct {
	values []string
}

// NewSecretRedactor builds a redactor from explicit tool env entries and the
// parent env values whose names match configured redact patterns.
func NewSecretRedactor(patterns []string, toolEnv []string) *SecretRedactor {
	seen := map[string]bool{}
	var values []string
	add := func(value string) {
		if len(value) < 4 || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	for _, entry := range toolEnv {
		_, value, ok := splitEnvEntry(entry)
		if ok {
			add(value)
		}
	}
	for _, entry := range os.Environ() {
		name, value, ok := splitEnvEntry(entry)
		if ok && envNameMatchesAny(name, patterns) {
			add(value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})
	return &SecretRedactor{values: values}
}

func (r *SecretRedactor) RedactText(text string) string {
	if r == nil || text == "" {
		return text
	}
	for _, value := range r.values {
		text = strings.ReplaceAll(text, value, redactedSecret)
	}
	return text
}

func splitEnvEntry(entry string) (string, string, bool) {
	name, value, ok := strings.Cut(entry, "=")
	if !ok || !ValidEnvName(name) {
		return "", "", false
	}
	return name, value, true
}

// ValidEnvName reports whether name is safe to use as an environment key.
func ValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 && r != '_' && !unicode.IsLetter(r) {
			return false
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func envNameMatchesAny(name string, patterns []string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	for _, pattern := range patterns {
		pattern = strings.ToUpper(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if ok, err := path.Match(pattern, name); err == nil && ok {
			return true
		}
		if pattern == name {
			return true
		}
	}
	return false
}

func appendGitHubCLIDiagnostic(cmd, stdout, stderr string, env []string) (string, string) {
	if !looksLikeGitHubCLICommand(cmd) {
		return stdout, stderr
	}
	diagnostic := GitHubCLIDiagnostic(stdout+"\n"+stderr, EnvNames(env))
	if diagnostic == "" {
		return stdout, stderr
	}
	if strings.TrimSpace(stderr) == "" {
		stderr = diagnostic + "\n"
	} else {
		stderr = strings.TrimRight(stderr, "\n") + "\n" + diagnostic + "\n"
	}
	return stdout, stderr
}

// GitHubCLIDiagnostic returns an actionable auth diagnostic for common gh errors.
func GitHubCLIDiagnostic(output string, envNames []string) string {
	normalized := strings.ToLower(output)
	if !strings.Contains(normalized, "gh auth") &&
		!strings.Contains(normalized, "gh_token") &&
		!strings.Contains(normalized, "github_token") &&
		!strings.Contains(normalized, "bad credentials") &&
		!strings.Contains(normalized, "authentication required") &&
		!strings.Contains(normalized, "resource not accessible by integration") &&
		!strings.Contains(normalized, "http 401") &&
		!strings.Contains(normalized, "http 403") {
		return ""
	}
	hasToken := false
	for _, name := range envNames {
		if name == "GH_TOKEN" || name == "GITHUB_TOKEN" {
			hasToken = true
			break
		}
	}
	switch {
	case strings.Contains(normalized, "resource not accessible by integration") || strings.Contains(normalized, "http 403"):
		return "GitHub CLI auth failed: the configured credential lacks permission for this operation. Use a token with the required repo/issues/pull-request scope or choose a connector/auth surface with write access."
	case hasToken && (strings.Contains(normalized, "bad credentials") || strings.Contains(normalized, "http 401")):
		return "GitHub CLI auth failed: the configured GH_TOKEN/GITHUB_TOKEN was rejected. Refresh the token, then pass it explicitly with [tools.auth.github] mode=\"env\" or [tools.env] allow=[\"GH_TOKEN\"]."
	case hasToken:
		return "GitHub CLI auth failed while using explicit env passthrough. Check token scopes and run `gh auth status` outside v100 if you need to inspect the credential."
	default:
		return "GitHub CLI auth unavailable in the agent shell. To enable gh issue/PR workflows without exposing unrelated secrets, export GH_TOKEN and set [tools.auth.github] mode=\"env\" env=\"GH_TOKEN\" or [tools.env] allow=[\"GH_TOKEN\"]."
	}
}

func looksLikeGitHubCLICommand(cmd string) bool {
	fields := strings.Fields(cmd)
	for _, field := range fields {
		field = strings.Trim(field, `"'`)
		base := field
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if base == "gh" {
			return true
		}
	}
	return false
}
