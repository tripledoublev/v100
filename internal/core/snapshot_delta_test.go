package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNoopSnapshotManager(t *testing.T) {
	mgr := NoopSnapshotManager{}
	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "call-1"})
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}
	if snap.ID != "noop-call-1" || snap.Method != "noop" {
		t.Fatalf("noop capture = %#v", snap)
	}
	restore, err := mgr.Restore(context.Background(), RestoreRequest{})
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if restore.SnapshotID != "noop" || restore.Method != "noop" {
		t.Fatalf("noop restore = %#v", restore)
	}
	async, err := mgr.CaptureAsync(context.Background(), SnapshotRequest{CallID: "async"})
	if err != nil {
		t.Fatalf("CaptureAsync returned error: %v", err)
	}
	if async.Result.ID != "noop-async" || async.Result.Method != "noop" {
		t.Fatalf("noop async capture = %#v", async.Result)
	}
	select {
	case err := <-async.Done:
		if err != nil {
			t.Fatalf("noop async returned error: %v", err)
		}
	default:
		t.Fatal("noop async did not complete immediately")
	}
}

func TestWorkspaceSnapshotDeltaRestoreAndDedup(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)

	writeSnapshotTestFile(t, workspace, "a.txt", "alpha\n")
	writeSnapshotTestFile(t, workspace, "b.txt", "shared\n")
	if err := os.MkdirAll(filepath.Join(workspace, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	firstState := readWorkspaceState(t, workspace)

	snap1, err := mgr.Capture(context.Background(), SnapshotRequest{StepID: "s1", CallID: "c1"})
	if err != nil {
		t.Fatalf("Capture 1 returned error: %v", err)
	}
	if snap1.Method != string(SnapshotModeDelta) {
		t.Fatalf("Capture 1 method = %q, want delta", snap1.Method)
	}
	manifest1 := readManifestForTest(t, snapshotRoot, snap1.ID)
	if manifest1.BaseID != "" {
		t.Fatalf("first snapshot base id = %q, want empty", manifest1.BaseID)
	}

	time.Sleep(time.Millisecond)
	writeSnapshotTestFile(t, workspace, "a.txt", "beta\n")
	if err := os.Remove(filepath.Join(workspace, "b.txt")); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, workspace, "nested/c.txt", "shared\n")
	secondState := readWorkspaceState(t, workspace)

	snap2, err := mgr.Capture(context.Background(), SnapshotRequest{StepID: "s2", CallID: "c2"})
	if err != nil {
		t.Fatalf("Capture 2 returned error: %v", err)
	}
	manifest2 := readManifestForTest(t, snapshotRoot, snap2.ID)
	if manifest2.BaseID != snap1.ID {
		t.Fatalf("second snapshot base id = %q, want %q", manifest2.BaseID, snap1.ID)
	}
	if hasManifestPath(manifest2, "b.txt") {
		t.Fatal("deleted file b.txt is still present in delta manifest")
	}
	if !hasManifestPath(manifest2, "nested/c.txt") {
		t.Fatal("added file nested/c.txt missing from delta manifest")
	}

	if got := countContentBlobs(t, snapshotRoot); got != 3 {
		t.Fatalf("content blob count = %d, want 3 unique contents", got)
	}

	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap1.ID}); err != nil {
		t.Fatalf("Restore 1 returned error: %v", err)
	}
	if got := readWorkspaceState(t, workspace); !sameWorkspaceState(got, firstState) {
		t.Fatalf("restore 1 mismatch\ngot:  %#v\nwant: %#v", got, firstState)
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap2.ID}); err != nil {
		t.Fatalf("Restore 2 returned error: %v", err)
	}
	if got := readWorkspaceState(t, workspace); !sameWorkspaceState(got, secondState) {
		t.Fatalf("restore 2 mismatch\ngot:  %#v\nwant: %#v", got, secondState)
	}
}

func TestWorkspaceSnapshotDeltaDetectsChangedContentWithRestoredMtime(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	writeSnapshotTestFile(t, workspace, "state.txt", "before")
	path := filepath.Join(workspace, "state.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ts := info.ModTime()
	if _, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "before"}); err != nil {
		t.Fatalf("Capture before returned error: %v", err)
	}

	writeSnapshotTestFile(t, workspace, "state.txt", "after!")
	if err := os.Chtimes(path, ts, ts); err != nil {
		t.Fatal(err)
	}
	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "after"})
	if err != nil {
		t.Fatalf("Capture after returned error: %v", err)
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "after!" {
		t.Fatalf("restored data = %q, want changed content", data)
	}
}

func TestWorkspaceSnapshotDeltaRehashesWhenChangeTimeUnavailable(t *testing.T) {
	previous := snapshotFile{
		Path:    "state.txt",
		Digest:  "abc123",
		Size:    6,
		Mode:    0o644,
		ModTime: 1234,
		CTimeOK: false,
	}
	current := snapshotFile{
		Path:    "state.txt",
		Size:    6,
		Mode:    0o644,
		ModTime: 1234,
		CTimeOK: false,
	}
	if sameSnapshotFileState(previous, current) {
		t.Fatal("mtime-only snapshot state was treated as unchanged")
	}
}

func TestWorkspaceSnapshotDeltaRestoresSymlinks(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	writeSnapshotTestFile(t, workspace, "target.txt", "target\n")
	linkPath := filepath.Join(workspace, "link.txt")
	if err := os.Symlink("target.txt", linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "links"})
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}
	manifest := readManifestForTest(t, snapshotRoot, snap.ID)
	if len(manifest.Links) != 1 || manifest.Links[0].Path != "link.txt" || manifest.Links[0].Target != "target.txt" {
		t.Fatalf("manifest links = %#v", manifest.Links)
	}

	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, workspace, "link.txt", "regular\n")
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link.txt mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if target != "target.txt" {
		t.Fatalf("symlink target = %q, want target.txt", target)
	}
}

func TestWorkspaceSnapshotDeltaRestoresFilesInsideRestrictiveDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode-only directory permissions are not portable on windows")
	}
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	dir := filepath.Join(workspace, "readonly")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, workspace, "readonly/state.txt", "locked\n")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()

	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "readonly"})
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clearDir(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "readonly", "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "locked\n" {
		t.Fatalf("restored data = %q, want locked", data)
	}
	info, err := os.Stat(filepath.Join(workspace, "readonly"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o555 {
		t.Fatalf("restored directory mode = %v, want 0555", got)
	}
}

func TestWorkspaceSnapshotValidationAndErrors(t *testing.T) {
	empty := &WorkspaceSnapshotManager{}
	if _, err := empty.Capture(context.Background(), SnapshotRequest{}); err == nil {
		t.Fatal("Capture returned nil error for empty manager")
	}
	if _, err := empty.CaptureAsync(context.Background(), SnapshotRequest{}); err == nil {
		t.Fatal("CaptureAsync returned nil error for empty manager")
	}

	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	if _, err := mgr.Restore(context.Background(), RestoreRequest{}); err == nil {
		t.Fatal("Restore returned nil error for empty snapshot id")
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: "missing"}); err == nil {
		t.Fatal("Restore returned nil error for missing snapshot")
	}

	badSnapshot := filepath.Join(snapshotRoot, "bad")
	if err := os.MkdirAll(badSnapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badSnapshot, snapshotManifestFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badSnapshot, snapshotMarkerFile), []byte(snapshotMarkerValue+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: "bad"}); err == nil {
		t.Fatal("Restore returned nil error for malformed manifest")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mgr.Capture(ctx, SnapshotRequest{CallID: "canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Capture error = %v, want context canceled", err)
	}
}

func TestWorkspaceSnapshotManifestValidation(t *testing.T) {
	dir := t.TempDir()
	if err := writeSnapshotManifest(dir, &snapshotManifest{Version: snapshotFormat, ID: "ok", Method: string(SnapshotModeDelta)}); err != nil {
		t.Fatalf("writeSnapshotManifest returned error: %v", err)
	}
	if _, err := readSnapshotManifest(dir); err != nil {
		t.Fatalf("readSnapshotManifest returned error: %v", err)
	}
	if err := writeSnapshotManifest(dir, &snapshotManifest{Version: 999, ID: "bad"}); err != nil {
		t.Fatalf("writeSnapshotManifest returned error: %v", err)
	}
	if _, err := readSnapshotManifest(dir); err == nil {
		t.Fatal("readSnapshotManifest returned nil error for unsupported version")
	}
}

func TestWorkspaceSnapshotContextAwareCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyWithContext(ctx, strings.NewReader("data"), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyWithContext error = %v, want context canceled", err)
	}
	var out bytes.Buffer
	var hash bytes.Buffer
	if _, err := copyHashingWithContext(ctx, strings.NewReader("data"), &out, &hash); !errors.Is(err, context.Canceled) {
		t.Fatalf("copyHashingWithContext error = %v, want context canceled", err)
	}
}

func TestSanitizeSnapshotComponent(t *testing.T) {
	got := sanitizeSnapshotComponent(" step/with\\spaces ")
	if got != "step-with-spaces" {
		t.Fatalf("sanitizeSnapshotComponent() = %q", got)
	}
	if got := sanitizeSnapshotComponent(" "); got != "" {
		t.Fatalf("sanitizeSnapshotComponent(blank) = %q, want empty", got)
	}
}

func TestWorkspaceSnapshotFullCopyMode(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManagerWithOptions(workspace, snapshotRoot, WorkspaceSnapshotOptions{Mode: SnapshotModeFullCopy})
	writeSnapshotTestFile(t, workspace, "state.txt", "before\n")

	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "full"})
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}
	if snap.Method != string(SnapshotModeFullCopy) {
		t.Fatalf("method = %q, want full_copy", snap.Method)
	}
	writeSnapshotTestFile(t, workspace, "state.txt", "after\n")
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" {
		t.Fatalf("restored state = %q, want before", data)
	}
}

func TestWorkspaceSnapshotFullCopyAllowsRootManifestFile(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManagerWithOptions(workspace, snapshotRoot, WorkspaceSnapshotOptions{Mode: SnapshotModeFullCopy})
	if err := os.WriteFile(filepath.Join(workspace, snapshotManifestFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "full"})
	if err != nil {
		t.Fatalf("Capture returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, snapshotManifestFile), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: snap.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, snapshotManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{bad" {
		t.Fatalf("restored marker-like file = %q, want original", data)
	}
}

func TestWorkspaceSnapshotCaptureAsync(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	writeSnapshotTestFile(t, workspace, "state.txt", "async\n")

	async, err := mgr.CaptureAsync(context.Background(), SnapshotRequest{CallID: "async"})
	if err != nil {
		t.Fatalf("CaptureAsync returned error: %v", err)
	}
	if !strings.HasSuffix(async.Result.Method, "_async") {
		t.Fatalf("async method = %q, want *_async", async.Result.Method)
	}
	select {
	case err := <-async.Done:
		if err != nil {
			t.Fatalf("async snapshot failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async snapshot")
	}
	if _, err := mgr.Restore(context.Background(), RestoreRequest{SnapshotID: async.Result.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
}

func TestWorkspaceSnapshotRestoreWaitsForPendingAsyncCapture(t *testing.T) {
	workspace := t.TempDir()
	snapshotRoot := filepath.Join(t.TempDir(), "snapshots")
	mgr := NewWorkspaceSnapshotManager(workspace, snapshotRoot)
	writeSnapshotTestFile(t, workspace, "state.txt", "async\n")

	async, err := mgr.CaptureAsync(context.Background(), SnapshotRequest{CallID: "async"})
	if err != nil {
		t.Fatalf("CaptureAsync returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := mgr.Restore(ctx, RestoreRequest{SnapshotID: async.Result.ID}); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "async\n" {
		t.Fatalf("restored data = %q, want async", data)
	}
}

func BenchmarkWorkspaceSnapshotFullVsDelta100MB(b *testing.B) {
	workspace := b.TempDir()
	for i := 0; i < 100; i++ {
		name := filepath.Join(workspace, "data", "file-"+leftPad3(i)+".bin")
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(name, bytes.Repeat([]byte{byte(i)}, 1024*1024), 0o644); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("full_copy", func(b *testing.B) {
		mgr := NewWorkspaceSnapshotManagerWithOptions(workspace, filepath.Join(b.TempDir(), "snapshots"), WorkspaceSnapshotOptions{Mode: SnapshotModeFullCopy})
		for i := 0; i < b.N; i++ {
			if _, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: strconv.Itoa(i)}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("delta_one_file_changed", func(b *testing.B) {
		mgr := NewWorkspaceSnapshotManager(workspace, filepath.Join(b.TempDir(), "snapshots"))
		if _, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: "base"}); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			writeSnapshotTestFile(b, workspace, "data/file-000.bin", "changed-"+strconv.Itoa(i))
			if _, err := mgr.Capture(context.Background(), SnapshotRequest{CallID: strconv.Itoa(i)}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func writeSnapshotTestFile(t testing.TB, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readManifestForTest(t *testing.T, root, id string) *snapshotManifest {
	t.Helper()
	manifest, err := readSnapshotManifest(filepath.Join(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func hasManifestPath(manifest *snapshotManifest, path string) bool {
	for _, f := range manifest.Files {
		if f.Path == path {
			return true
		}
	}
	return false
}

func countContentBlobs(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, snapshotContentDir))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".tmp-") {
			count++
		}
	}
	return count
}

func readWorkspaceState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
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
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			state[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func sameWorkspaceState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}

func leftPad3(i int) string {
	s := strconv.Itoa(i)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
