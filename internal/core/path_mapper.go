package core

import (
	"path/filepath"
	"sort"
	"strings"
)

// PathMapper handles translation between Host, Sandbox, and Virtual roots.
type PathMapper struct {
	HostRoot    string // Original source workspace
	SandboxRoot string // Isolated sandbox directory
	VirtualRoot string // Always "/workspace" for the model
}

func NewPathMapper(host, sandbox string) *PathMapper {
	return &PathMapper{
		HostRoot:    host,
		SandboxRoot: sandbox,
		VirtualRoot: "/workspace",
	}
}

// ToSandbox takes a path from the model (virtual or relative) and returns the absolute host sandbox path.
func (m *PathMapper) ToSandbox(path string) string {
	path = strings.TrimPrefix(path, m.VirtualRoot)
	path = strings.TrimPrefix(path, "/")

	// If it's still absolute, it's a jailbreak attempt or malformed virtual path.
	if filepath.IsAbs(path) {
		// Strictly enforce relative-to-root for the sandbox.
		// We take only the base/remainder if someone tried /etc/shadow.
		_, path = filepath.Split(path)
	}

	return filepath.Join(m.SandboxRoot, path)
}

// ToVirtual takes a host path (source or sandbox) and returns the stable /workspace path.
func (m *PathMapper) ToVirtual(path string) string {
	if m.SandboxRoot != "" && strings.HasPrefix(path, m.SandboxRoot) {
		rel, err := filepath.Rel(m.SandboxRoot, path)
		if err != nil {
			return path
		}
		if rel == "." {
			return m.VirtualRoot
		}
		return filepath.Join(m.VirtualRoot, rel)
	}
	if m.HostRoot != "" && strings.HasPrefix(path, m.HostRoot) {
		rel, err := filepath.Rel(m.HostRoot, path)
		if err != nil {
			return path
		}
		if rel == "." {
			return m.VirtualRoot
		}
		return filepath.Join(m.VirtualRoot, rel)
	}
	return path
}

// SanitizeText rewrites host workspace paths to the stable /workspace namespace.
func (m *PathMapper) SanitizeText(text string) string {
	if text == "" {
		return text
	}

	var roots []string
	for _, root := range []string{m.SandboxRoot, m.HostRoot} {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." {
			continue
		}
		duplicate := false
		for _, existing := range roots {
			if existing == root {
				duplicate = true
				break
			}
		}
		if !duplicate {
			roots = append(roots, root)
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		return len(roots[i]) > len(roots[j])
	})

	out := text
	for _, root := range roots {
		out = replacePathRoot(out, root, m.VirtualRoot)
	}
	out = strings.ReplaceAll(out, m.VirtualRoot+"\\", m.VirtualRoot+"/")
	return out
}

// SecurePath returns the sandbox path only if it stays within boundaries.
func (m *PathMapper) SecurePath(path string) (string, bool) {
	target := m.ToSandbox(path)
	rel, err := filepath.Rel(m.SandboxRoot, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return target, true
}

func replacePathRoot(text, root, replacement string) string {
	if root == "" {
		return text
	}

	var b strings.Builder
	searchFrom := 0
	for {
		idx := strings.Index(text[searchFrom:], root)
		if idx == -1 {
			b.WriteString(text[searchFrom:])
			return b.String()
		}
		idx += searchFrom
		next := idx + len(root)
		if !isPathBoundary(text, next) {
			b.WriteString(text[searchFrom:next])
			searchFrom = next
			continue
		}
		b.WriteString(text[searchFrom:idx])
		b.WriteString(replacement)
		searchFrom = next
	}
}

func isPathBoundary(text string, idx int) bool {
	if idx >= len(text) {
		return true
	}
	switch text[idx] {
	case '/', '\\', ':', ' ', '\n', '\r', '\t', '"', '\'', ',', ';', ')', ']', '}', '>':
		return true
	default:
		return false
	}
}
