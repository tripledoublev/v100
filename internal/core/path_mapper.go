package core

import (
	"path/filepath"
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
		rel, _ := filepath.Rel(m.SandboxRoot, path)
		if rel == "." {
			return m.VirtualRoot
		}
		return filepath.Join(m.VirtualRoot, rel)
	}
	if m.HostRoot != "" && strings.HasPrefix(path, m.HostRoot) {
		rel, _ := filepath.Rel(m.HostRoot, path)
		if rel == "." {
			return m.VirtualRoot
		}
		return filepath.Join(m.VirtualRoot, rel)
	}
	return path
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
