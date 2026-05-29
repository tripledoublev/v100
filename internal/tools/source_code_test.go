package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type strictSourceMapper struct {
	root string
}

func (m strictSourceMapper) ToSandbox(path string) string {
	path = strings.TrimPrefix(path, "/workspace")
	path = strings.TrimPrefix(path, "/")
	if filepath.IsAbs(path) {
		_, path = filepath.Split(path)
	}
	return filepath.Join(m.root, path)
}
func (m strictSourceMapper) ToVirtual(path string) string { return path }
func (m strictSourceMapper) SanitizeText(text string) string {
	return text
}
func (m strictSourceMapper) SecurePath(path string) (string, bool) {
	target := m.ToSandbox(path)
	rel, err := filepath.Rel(m.root, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return target, true
}

func sourceCall(dir string) ToolCallContext {
	return ToolCallContext{
		WorkspaceDir: dir,
		Mapper:       strictSourceMapper{root: dir},
	}
}

func sourceArgs(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSourceCodeReadsGoModuleCachePackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\nrequire golang.org/x/example v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, ".gomodcache", "golang.org", "x", "example@v1.2.3", "sub")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "pkg.go"), []byte("package sub\n\nfunc CacheHit() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "read",
		"ecosystem": "go",
		"package":   "golang.org/x/example/sub",
		"path":      "pkg.go",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("source_code read failed: %s", res.Output)
	}
	for _, want := range []string{"Go module cache", `"version": "v1.2.3"`, "func CacheHit"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Output)
		}
	}
	if res.TaintLevel != "external_dependency_source" {
		t.Fatalf("taint = %q, want external_dependency_source", res.TaintLevel)
	}

	resolveRes, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "resolve",
		"ecosystem": "go",
		"package":   "golang.org/x/example/sub",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !resolveRes.OK || !strings.Contains(resolveRes.Output, `"package_dir"`) {
		t.Fatalf("resolve output unexpected:\n%s", resolveRes.Output)
	}

	listRes, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "list",
		"ecosystem": "go",
		"package":   "golang.org/x/example/sub",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !listRes.OK || !strings.Contains(listRes.Output, `"name": "pkg.go"`) {
		t.Fatalf("list output unexpected:\n%s", listRes.Output)
	}
}

func TestSourceCodeSearchesCurrentGoModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "thing")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "thing.go"), []byte("package thing\n\nfunc LocalSymbol() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "search",
		"ecosystem": "go",
		"package":   "example.com/app/internal/thing",
		"pattern":   "LocalSymbol",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("source_code search failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "thing.go:3:func LocalSymbol") {
		t.Fatalf("search output missing match:\n%s", res.Output)
	}
	if res.TaintLevel != "workspace_data" {
		t.Fatalf("taint = %q, want workspace_data", res.TaintLevel)
	}
}

func TestSourceCodeReadsNPMAndPyPIPackages(t *testing.T) {
	dir := t.TempDir()
	npmRoot := filepath.Join(dir, "node_modules", "@scope", "pkg")
	if err := os.MkdirAll(filepath.Join(npmRoot, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(npmRoot, "package.json"), []byte(`{"version":"4.5.6"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(npmRoot, "lib", "index.js"), []byte("export const scoped = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pyRoot := filepath.Join(dir, ".venv", "lib", "python3.12", "site-packages", "requests")
	if err := os.MkdirAll(pyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	pyMeta := filepath.Join(dir, ".venv", "lib", "python3.12", "site-packages", "requests-2.31.0.dist-info")
	if err := os.MkdirAll(pyMeta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pyMeta, "METADATA"), []byte("Name: requests\nVersion: 2.31.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pyRoot, "sessions.py"), []byte("class Session:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	npmRes, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "read",
		"ecosystem": "npm",
		"package":   "@scope/pkg/lib",
		"path":      "index.js",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !npmRes.OK || !strings.Contains(npmRes.Output, "export const scoped") || !strings.Contains(npmRes.Output, `"version": "4.5.6"`) {
		t.Fatalf("npm read output unexpected:\n%s", npmRes.Output)
	}

	pyRes, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "search",
		"ecosystem": "pypi",
		"package":   "requests",
		"version":   "2.31.0",
		"pattern":   "Session",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !pyRes.OK || !strings.Contains(pyRes.Output, "sessions.py:1:class Session") || !strings.Contains(pyRes.Output, `"version": "2.31.0"`) {
		t.Fatalf("pypi search output unexpected:\n%s", pyRes.Output)
	}

	mismatchRes, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "read",
		"ecosystem": "npm",
		"package":   "@scope/pkg/lib",
		"version":   "0.0.0",
		"path":      "index.js",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if mismatchRes.OK {
		t.Fatalf("expected npm version mismatch to fail, got:\n%s", mismatchRes.Output)
	}
}

func TestSourceCodeReadsCrateCache(t *testing.T) {
	dir := t.TempDir()
	crateRoot := filepath.Join(dir, ".cargo", "registry", "src", "index.crates.io-abc", "serde-1.0.200")
	if err := os.MkdirAll(filepath.Join(crateRoot, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crateRoot, "src", "lib.rs"), []byte("pub fn crate_symbol() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "read",
		"ecosystem": "crates",
		"package":   "serde",
		"version":   "1.0.200",
		"path":      "src/lib.rs",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !strings.Contains(res.Output, "pub fn crate_symbol") || !strings.Contains(res.Output, `"version": "1.0.200"`) {
		t.Fatalf("crate read output unexpected:\n%s", res.Output)
	}
}

func TestSourceCodeRejectsReadPathEscape(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, ".gomodcache", "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := SourceCode().Exec(context.Background(), sourceCall(dir), sourceArgs(t, map[string]any{
		"action":    "read",
		"ecosystem": "go",
		"package":   "example.com/dep",
		"path":      "../other.go",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatalf("expected path escape to fail, got:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "escapes package source") {
		t.Fatalf("expected escape error, got: %s", res.Output)
	}
}
