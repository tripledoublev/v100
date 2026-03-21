package core

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

const workspaceIgnoreFile = ".v100ignore"

type workspaceFilter struct {
	custom []string
}

func newWorkspaceFilter(root string) workspaceFilter {
	return workspaceFilter{
		custom: loadWorkspaceIgnorePatterns(root),
	}
}

func (f workspaceFilter) Skip(rel string, info os.FileInfo) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return false
	}
	if shouldSkipBuiltInWorkspacePath(rel) {
		return true
	}
	for _, pattern := range f.custom {
		if workspaceIgnoreMatch(pattern, rel) {
			return true
		}
	}
	return false
}

func shouldSkipBuiltInWorkspacePath(rel string) bool {
	// Harness runtime byproducts
	if rel == "runs" || strings.HasPrefix(rel, "runs/") {
		return true
	}
	if rel == "exports" || strings.HasPrefix(rel, "exports/") {
		return true
	}

	// General caches and package manager noise
	if rel == ".cache" || strings.HasPrefix(rel, ".cache/") {
		return true
	}
	if rel == ".gocache" || strings.HasPrefix(rel, ".gocache/") {
		return true
	}
	if rel == ".gomodcache" || strings.HasPrefix(rel, ".gomodcache/") {
		return true
	}
	if rel == ".npm" || strings.HasPrefix(rel, ".npm/") {
		return true
	}
	if rel == "node_modules" || strings.HasPrefix(rel, "node_modules/") {
		return true
	}

	// Tool-specific noise
	if rel == ".config" || rel == ".config/go" {
		return true
	}
	if rel == ".config/go/telemetry" || strings.HasPrefix(rel, ".config/go/telemetry/") {
		return true
	}
	return false
}

func loadWorkspaceIgnorePatterns(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root, workspaceIgnoreFile))
	if err != nil {
		return nil
	}
	return parseWorkspaceIgnorePatterns(string(b))
}

func parseWorkspaceIgnorePatterns(content string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimPrefix(line, "./")
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "/") {
			base := strings.TrimSuffix(line, "/")
			if base == "" {
				continue
			}
			add(base + "/**")
			add("**/" + base + "/**")
			continue
		}
		if !strings.Contains(line, "/") && !strings.ContainsAny(line, "*?[]") {
			add(line)
			add("**/" + line)
			add("**/" + line + "/**")
			continue
		}
		add(line)
	}
	return out
}

func workspaceIgnoreMatch(pattern, rel string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "**/") {
		rest := strings.TrimPrefix(pattern, "**/")
		if strings.HasSuffix(rest, "/**") {
			base := strings.TrimSuffix(rest, "/**")
			return rel == base || strings.HasPrefix(rel, base+"/") || strings.Contains(rel, "/"+base+"/")
		}
		if matched, _ := path.Match(rest, rel); matched {
			return true
		}
		return rel == rest || strings.HasSuffix(rel, "/"+rest)
	}
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		return rel == base || strings.HasPrefix(rel, base+"/")
	}
	matched, err := path.Match(pattern, rel)
	return err == nil && matched
}
