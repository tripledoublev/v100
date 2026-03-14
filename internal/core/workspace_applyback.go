package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkspaceDiff describes sandbox-visible changes relative to the source workspace.
type WorkspaceDiff struct {
	Added    []string          `json:"added,omitempty"`
	Modified []string          `json:"modified,omitempty"`
	Deleted  []string          `json:"deleted,omitempty"`
	Previews map[string]string `json:"previews,omitempty"`
}

// Empty reports whether the diff contains any file or directory changes.
func (d WorkspaceDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Modified) == 0 && len(d.Deleted) == 0
}

// SandboxFinalizeOptions controls sandbox export/apply-back behavior.
type SandboxFinalizeOptions struct {
	Mode                string
	Success             bool
	SourceWorkspace     string
	SandboxWorkspace    string
	BaselineFingerprint string
	ArtifactDir         string
}

// SandboxFinalizeResult reports the exported diff and whether it was applied back.
type SandboxFinalizeResult struct {
	Mode              string        `json:"mode"`
	Success           bool          `json:"success"`
	Applied           bool          `json:"applied"`
	Conflict          bool          `json:"conflict,omitempty"`
	SkippedReason     string        `json:"skipped_reason,omitempty"`
	ArtifactPath      string        `json:"artifact_path,omitempty"`
	SourceFingerprint string        `json:"source_fingerprint,omitempty"`
	Diff              WorkspaceDiff `json:"diff"`
}

type workspaceEntry struct {
	Rel    string
	Abs    string
	Mode   os.FileMode
	IsDir  bool
	Digest string
}

// WorkspaceFingerprint computes a stable fingerprint for a workspace tree.
func WorkspaceFingerprint(root string) (string, error) {
	entries, err := scanWorkspace(root)
	if err != nil {
		return "", err
	}

	keys := sortedEntryKeys(entries)
	h := sha256.New()
	for _, key := range keys {
		entry := entries[key]
		_, _ = io.WriteString(h, key)
		_, _ = io.WriteString(h, "\x00")
		if entry.IsDir {
			_, _ = io.WriteString(h, "dir")
		} else {
			_, _ = io.WriteString(h, "file")
		}
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, entry.Mode.String())
		_, _ = io.WriteString(h, "\x00")
		_, _ = io.WriteString(h, entry.Digest)
		_, _ = io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FinalizeSandboxWorkspace exports sandbox changes and optionally applies them back.
func FinalizeSandboxWorkspace(opts SandboxFinalizeOptions) (SandboxFinalizeResult, error) {
	mode := normalizeApplyBackMode(opts.Mode)
	if strings.TrimSpace(opts.SourceWorkspace) == "" || strings.TrimSpace(opts.SandboxWorkspace) == "" {
		return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: source and sandbox workspaces are required")
	}

	sourceEntries, err := scanWorkspace(opts.SourceWorkspace)
	if err != nil {
		return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: scan source: %w", err)
	}
	sandboxEntries, err := scanWorkspace(opts.SandboxWorkspace)
	if err != nil {
		return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: scan sandbox: %w", err)
	}

	diff := diffWorkspaceEntries(sourceEntries, sandboxEntries)
	result := SandboxFinalizeResult{
		Mode:    mode,
		Success: opts.Success,
		Diff:    diff,
	}

	switch mode {
	case "never":
		result.SkippedReason = "apply_back_disabled"
	case "manual":
		result.SkippedReason = "manual_review_required"
	case "on_success":
		if !opts.Success {
			result.SkippedReason = "run_not_successful"
			break
		}
		if diff.Empty() {
			result.SkippedReason = "no_changes"
			break
		}
		if strings.TrimSpace(opts.BaselineFingerprint) == "" {
			result.SkippedReason = "missing_source_fingerprint"
			break
		}
		currentFingerprint, err := WorkspaceFingerprint(opts.SourceWorkspace)
		if err != nil {
			return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: fingerprint source: %w", err)
		}
		result.SourceFingerprint = currentFingerprint
		if currentFingerprint != opts.BaselineFingerprint {
			result.Conflict = true
			result.SkippedReason = "source_workspace_changed"
			break
		}
		if err := applyWorkspaceChanges(opts.SourceWorkspace, sourceEntries, sandboxEntries, diff); err != nil {
			return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: apply back: %w", err)
		}
		result.Applied = true
	default:
		return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: unknown apply_back mode %q", opts.Mode)
	}

	if strings.TrimSpace(opts.ArtifactDir) != "" {
		result.ArtifactPath = filepath.Join(opts.ArtifactDir, "sandbox_apply_back.json")
		if err := writeSandboxFinalizeArtifact(result.ArtifactPath, result); err != nil {
			return SandboxFinalizeResult{}, fmt.Errorf("sandbox finalize: write artifact: %w", err)
		}
	}

	return result, nil
}

func normalizeApplyBackMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual":
		return "manual"
	case "on_success":
		return "on_success"
	case "never":
		return "never"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func writeSandboxFinalizeArtifact(path string, result SandboxFinalizeResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	return nil
}

func scanWorkspace(root string) (map[string]workspaceEntry, error) {
	root = filepath.Clean(root)
	entries := make(map[string]workspaceEntry)
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldSkipWorkspacePath(rel, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		entry := workspaceEntry{
			Rel:   rel,
			Abs:   path,
			Mode:  info.Mode(),
			IsDir: info.IsDir(),
		}
		if !info.IsDir() {
			digest, err := fileDigest(path)
			if err != nil {
				return err
			}
			entry.Digest = digest
		}
		entries[rel] = entry
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func shouldSkipWorkspacePath(rel string, info os.FileInfo) bool {
	rel = filepath.ToSlash(rel)
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

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func diffWorkspaceEntries(sourceEntries, sandboxEntries map[string]workspaceEntry) WorkspaceDiff {
	keys := make(map[string]struct{}, len(sourceEntries)+len(sandboxEntries))
	for key := range sourceEntries {
		keys[key] = struct{}{}
	}
	for key := range sandboxEntries {
		keys[key] = struct{}{}
	}

	all := make([]string, 0, len(keys))
	for key := range keys {
		all = append(all, key)
	}
	sort.Strings(all)

	diff := WorkspaceDiff{}
	for _, key := range all {
		src, srcOK := sourceEntries[key]
		sand, sandOK := sandboxEntries[key]
		switch {
		case !srcOK && sandOK:
			diff.Added = append(diff.Added, key)
		case srcOK && !sandOK:
			diff.Deleted = append(diff.Deleted, key)
		case src.IsDir != sand.IsDir:
			diff.Modified = append(diff.Modified, key)
		case src.IsDir && sand.IsDir:
			if src.Mode.Perm() != sand.Mode.Perm() {
				diff.Modified = append(diff.Modified, key)
			}
		case src.Digest != sand.Digest || src.Mode.Perm() != sand.Mode.Perm():
			diff.Modified = append(diff.Modified, key)
		}
	}

	// Populate content previews for small added/modified files (≤4KB).
	const maxPreviewFile = 4096
	const maxPreviewChars = 512
	changed := append(diff.Added, diff.Modified...)
	for _, key := range changed {
		sand, ok := sandboxEntries[key]
		if !ok || sand.IsDir {
			continue
		}
		info, err := os.Stat(sand.Abs)
		if err != nil || info.Size() > maxPreviewFile {
			continue
		}
		data, err := os.ReadFile(sand.Abs)
		if err != nil {
			continue
		}
		preview := string(data)
		if len(preview) > maxPreviewChars {
			preview = preview[:maxPreviewChars] + "…"
		}
		if diff.Previews == nil {
			diff.Previews = make(map[string]string)
		}
		diff.Previews[key] = preview
	}

	return diff
}

func applyWorkspaceChanges(sourceRoot string, sourceEntries, sandboxEntries map[string]workspaceEntry, diff WorkspaceDiff) error {
	removals := removalPaths(sourceEntries, sandboxEntries, diff)
	sort.Slice(removals, func(i, j int) bool {
		return pathDepth(removals[i]) > pathDepth(removals[j])
	})
	for _, rel := range removals {
		if err := os.RemoveAll(filepath.Join(sourceRoot, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}

	dirs := dirsToCreate(sourceEntries, sandboxEntries, diff)
	sort.Slice(dirs, func(i, j int) bool {
		return pathDepth(dirs[i].Rel) < pathDepth(dirs[j].Rel)
	})
	for _, dir := range dirs {
		target := filepath.Join(sourceRoot, filepath.FromSlash(dir.Rel))
		if err := os.MkdirAll(target, dir.Mode.Perm()); err != nil {
			return err
		}
		if err := os.Chmod(target, dir.Mode.Perm()); err != nil {
			return err
		}
	}

	files := filesToCopy(sourceEntries, sandboxEntries, diff)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Rel < files[j].Rel
	})
	for _, file := range files {
		target := filepath.Join(sourceRoot, filepath.FromSlash(file.Rel))
		if err := copySnapshotFile(file.Abs, target, file.Mode); err != nil {
			return err
		}
	}
	return nil
}

func removalPaths(sourceEntries, sandboxEntries map[string]workspaceEntry, diff WorkspaceDiff) []string {
	seen := make(map[string]struct{})
	var removals []string
	add := func(rel string) {
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		removals = append(removals, rel)
	}

	for _, rel := range diff.Deleted {
		add(rel)
	}
	for _, rel := range diff.Modified {
		src, srcOK := sourceEntries[rel]
		sand, sandOK := sandboxEntries[rel]
		if srcOK && sandOK && src.IsDir != sand.IsDir {
			add(rel)
		}
	}
	return removals
}

func dirsToCreate(sourceEntries, sandboxEntries map[string]workspaceEntry, diff WorkspaceDiff) []workspaceEntry {
	wanted := changedPathSet(diff)
	var dirs []workspaceEntry
	for rel, sand := range sandboxEntries {
		if !sand.IsDir {
			continue
		}
		if _, ok := wanted[rel]; !ok {
			continue
		}
		src, srcOK := sourceEntries[rel]
		if !srcOK || !src.IsDir || src.Mode.Perm() != sand.Mode.Perm() {
			dirs = append(dirs, sand)
		}
	}
	return dirs
}

func filesToCopy(sourceEntries, sandboxEntries map[string]workspaceEntry, diff WorkspaceDiff) []workspaceEntry {
	wanted := changedPathSet(diff)
	var files []workspaceEntry
	for rel, sand := range sandboxEntries {
		if sand.IsDir {
			continue
		}
		if _, ok := wanted[rel]; !ok {
			continue
		}
		src, srcOK := sourceEntries[rel]
		if !srcOK || src.IsDir || src.Digest != sand.Digest || src.Mode.Perm() != sand.Mode.Perm() {
			files = append(files, sand)
		}
	}
	return files
}

func changedPathSet(diff WorkspaceDiff) map[string]struct{} {
	paths := make(map[string]struct{}, len(diff.Added)+len(diff.Modified))
	for _, rel := range diff.Added {
		paths[rel] = struct{}{}
	}
	for _, rel := range diff.Modified {
		paths[rel] = struct{}{}
	}
	return paths
}

func sortedEntryKeys(entries map[string]workspaceEntry) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func pathDepth(rel string) int {
	if rel == "" {
		return 0
	}
	return strings.Count(rel, "/") + 1
}
