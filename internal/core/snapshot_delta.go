package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	snapshotManifestFile = ".v100snapshot.json"
	snapshotMarkerFile   = ".v100snapshot.format"
	snapshotMarkerValue  = "v100-delta-snapshot-v1"
	snapshotContentDir   = "content"
	snapshotFormat       = 1
)

type snapshotManifest struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	BaseID  string         `json:"base_id,omitempty"`
	Dirs    []snapshotDir  `json:"dirs,omitempty"`
	Files   []snapshotFile `json:"files,omitempty"`
	Links   []snapshotLink `json:"links,omitempty"`
}

type snapshotDir struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
}

type snapshotFile struct {
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	ModTime int64  `json:"mod_time_unix_nano"`
	CTime   int64  `json:"change_time_unix_nano,omitempty"`
	CTimeOK bool   `json:"change_time_available,omitempty"`
}

type snapshotLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func (m *WorkspaceSnapshotManager) captureDelta(ctx context.Context, id string) (SnapshotResult, error) {
	dst := filepath.Join(m.SnapshotRoot, id)
	if err := os.RemoveAll(dst); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: clear %s: %w", dst, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: mkdir %s: %w", dst, err)
	}
	contentRoot := m.contentRoot()
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: mkdir content store: %w", err)
	}

	baseByPath := map[string]snapshotFile{}
	baseID := ""
	if m.lastManifest != nil {
		baseID = m.lastManifest.ID
		for _, f := range m.lastManifest.Files {
			baseByPath[f.Path] = f
		}
	}

	manifest := &snapshotManifest{
		Version: snapshotFormat,
		ID:      id,
		Method:  string(SnapshotModeDelta),
		BaseID:  baseID,
	}
	filter := newWorkspaceFilter(m.WorkspaceDir)
	err := filepath.Walk(m.WorkspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(m.WorkspaceDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if filter.Skip(rel, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			manifest.Dirs = append(manifest.Dirs, snapshotDir{
				Path: rel,
				Mode: uint32(info.Mode().Perm()),
			})
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			manifest.Links = append(manifest.Links, snapshotLink{
				Path:   rel,
				Target: target,
			})
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		ctime, ctimeOK := fileChangeTime(info)
		file := snapshotFile{
			Path:    rel,
			Size:    info.Size(),
			Mode:    uint32(info.Mode().Perm()),
			ModTime: info.ModTime().UnixNano(),
			CTime:   ctime,
			CTimeOK: ctimeOK,
		}
		if previous, ok := baseByPath[rel]; ok && sameSnapshotFileState(previous, file) && m.contentExists(previous.Digest) {
			file.Digest = previous.Digest
		} else {
			digest, size, err := m.storeContent(ctx, path)
			if err != nil {
				return err
			}
			file.Digest = digest
			file.Size = size
		}
		manifest.Files = append(manifest.Files, file)
		return nil
	})
	if err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: capture %s: %w", id, err)
	}
	sort.Slice(manifest.Dirs, func(i, j int) bool { return manifest.Dirs[i].Path < manifest.Dirs[j].Path })
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	sort.Slice(manifest.Links, func(i, j int) bool { return manifest.Links[i].Path < manifest.Links[j].Path })

	if err := writeSnapshotManifest(dst, manifest); err != nil {
		return SnapshotResult{}, fmt.Errorf("workspace snapshot: write manifest: %w", err)
	}
	m.lastManifest = manifest
	return SnapshotResult{ID: id, Method: string(SnapshotModeDelta)}, nil
}

func (m *WorkspaceSnapshotManager) restoreDelta(ctx context.Context, manifest *snapshotManifest) error {
	if err := os.MkdirAll(m.WorkspaceDir, 0o755); err != nil {
		return fmt.Errorf("mkdir workspace: %w", err)
	}
	if err := clearDir(m.WorkspaceDir); err != nil {
		return fmt.Errorf("clear workspace: %w", err)
	}
	for _, dir := range manifest.Dirs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target := filepath.Join(m.WorkspaceDir, filepath.FromSlash(dir.Path))
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
	}
	for _, link := range manifest.Links {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		target := filepath.Join(m.WorkspaceDir, filepath.FromSlash(link.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(link.Target, target); err != nil {
			return err
		}
	}
	for _, file := range manifest.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		src := m.contentPath(file.Digest)
		target := filepath.Join(m.WorkspaceDir, filepath.FromSlash(file.Path))
		if err := copyFileWithContext(ctx, src, target, os.FileMode(file.Mode)); err != nil {
			return err
		}
		if file.ModTime > 0 {
			ts := time.Unix(0, file.ModTime)
			_ = os.Chtimes(target, ts, ts)
		}
	}
	for i := len(manifest.Dirs) - 1; i >= 0; i-- {
		dir := manifest.Dirs[i]
		target := filepath.Join(m.WorkspaceDir, filepath.FromSlash(dir.Path))
		_ = os.Chmod(target, os.FileMode(dir.Mode))
	}
	return nil
}

func sameSnapshotFileState(a, b snapshotFile) bool {
	if strings.TrimSpace(a.Digest) == "" {
		return false
	}
	if a.Size != b.Size || a.Mode != b.Mode || a.ModTime != b.ModTime {
		return false
	}
	if !a.CTimeOK || !b.CTimeOK {
		return false
	}
	return a.CTime == b.CTime
}

func (m *WorkspaceSnapshotManager) contentRoot() string {
	return filepath.Join(m.SnapshotRoot, snapshotContentDir)
}

func (m *WorkspaceSnapshotManager) contentPath(digest string) string {
	return filepath.Join(m.contentRoot(), digest)
}

func (m *WorkspaceSnapshotManager) contentExists(digest string) bool {
	if strings.TrimSpace(digest) == "" {
		return false
	}
	_, err := os.Stat(m.contentPath(digest))
	return err == nil
}

func (m *WorkspaceSnapshotManager) storeContent(ctx context.Context, src string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(m.contentRoot(), ".tmp-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	hash := sha256.New()
	size, err := copyHashingWithContext(ctx, in, tmp, hash)
	closeErr := tmp.Close()
	if err != nil {
		return "", 0, err
	}
	if closeErr != nil {
		return "", 0, closeErr
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	finalPath := m.contentPath(digest)
	if _, err := os.Stat(finalPath); err == nil {
		return digest, size, nil
	}
	if err := os.Chmod(tmpPath, 0o444); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", 0, err
	}
	return digest, size, nil
}

func copyHashingWithContext(ctx context.Context, in io.Reader, out io.Writer, hash io.Writer) (int64, error) {
	buf := make([]byte, 128*1024)
	var written int64
	for {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, err := hash.Write(chunk); err != nil {
				return written, err
			}
			if _, err := out.Write(chunk); err != nil {
				return written, err
			}
			written += int64(n)
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func copyFileWithContext(ctx context.Context, src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := copyWithContext(ctx, in, out); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func copyWithContext(ctx context.Context, in io.Reader, out io.Writer) (int64, error) {
	buf := make([]byte, 128*1024)
	var written int64
	for {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return written, err
			}
			written += int64(n)
		}
		if readErr == io.EOF {
			return written, nil
		}
		if readErr != nil {
			return written, readErr
		}
	}
}

func writeSnapshotManifest(snapshotDir string, manifest *snapshotManifest) error {
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, snapshotManifestFile), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(snapshotDir, snapshotMarkerFile), []byte(snapshotMarkerValue+"\n"), 0o644)
}

func isDeltaSnapshotDir(snapshotDir string) bool {
	b, err := os.ReadFile(filepath.Join(snapshotDir, snapshotMarkerFile))
	return err == nil && strings.TrimSpace(string(b)) == snapshotMarkerValue
}

func readSnapshotManifest(snapshotDir string) (*snapshotManifest, error) {
	b, err := os.ReadFile(filepath.Join(snapshotDir, snapshotManifestFile))
	if err != nil {
		return nil, err
	}
	var manifest snapshotManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return nil, err
	}
	if manifest.Version != snapshotFormat {
		return nil, fmt.Errorf("unsupported snapshot manifest version %d", manifest.Version)
	}
	return &manifest, nil
}
