package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type sourceCodeTool struct{}

// SourceCode creates a read-only dependency/package source inspection tool.
func SourceCode() Tool { return &sourceCodeTool{} }

func (t *sourceCodeTool) Name() string { return "source_code" }
func (t *sourceCodeTool) Description() string {
	return "Resolve, list, search, and read source files from local package/library sources such as the current module, vendor, .gomodcache, node_modules, virtualenv site-packages, and Cargo registry caches."
}
func (t *sourceCodeTool) DangerLevel() DangerLevel { return Safe }
func (t *sourceCodeTool) Effects() ToolEffects     { return ToolEffects{} }

func (t *sourceCodeTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["action", "package"],
		"properties": {
			"action": {"type": "string", "enum": ["resolve", "list", "search", "read"], "description": "Operation to perform."},
			"ecosystem": {"type": "string", "enum": ["auto", "go", "npm", "pypi", "crates", "path"], "description": "Package ecosystem. Defaults to auto.", "default": "auto"},
			"package": {"type": "string", "description": "Package name, import path, crate name, Python module, npm package, or workspace path."},
			"version": {"type": "string", "description": "Optional exact package version, when available in local caches."},
			"path": {"type": "string", "description": "Optional file or subdirectory path relative to the resolved package directory."},
			"pattern": {"type": "string", "description": "Regex pattern for action=search."},
			"start_line": {"type": "integer", "description": "Optional 1-based start line for action=read."},
			"end_line": {"type": "integer", "description": "Optional 1-based end line for action=read."},
			"max_results": {"type": "integer", "description": "Maximum search result lines.", "default": 80},
			"max_chars": {"type": "integer", "description": "Maximum output characters.", "default": 20000}
		}
	}`)
}

func (t *sourceCodeTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok": {"type": "boolean"},
			"action": {"type": "string"},
			"ecosystem": {"type": "string"},
			"package": {"type": "string"},
			"version": {"type": "string"},
			"root": {"type": "string"},
			"package_dir": {"type": "string"},
			"output": {"type": "string"}
		}
	}`)
}

type sourceCodeArgs struct {
	Action     string `json:"action"`
	Ecosystem  string `json:"ecosystem"`
	Package    string `json:"package"`
	Version    string `json:"version"`
	Path       string `json:"path"`
	Pattern    string `json:"pattern"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	MaxResults int    `json:"max_results"`
	MaxChars   int    `json:"max_chars"`
}

type sourceResolution struct {
	Ecosystem  string `json:"ecosystem"`
	Package    string `json:"package"`
	Version    string `json:"version,omitempty"`
	Root       string `json:"root"`
	PackageDir string `json:"package_dir"`
	Subdir     string `json:"subdir,omitempty"`
	Source     string `json:"source"`
	IsFile     bool   `json:"is_file,omitempty"`
}

func (t *sourceCodeTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var a sourceCodeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	a.Ecosystem = strings.ToLower(strings.TrimSpace(a.Ecosystem))
	a.Package = strings.TrimSpace(a.Package)
	a.Version = strings.TrimSpace(a.Version)
	a.Path = strings.TrimSpace(a.Path)
	if a.Ecosystem == "" {
		a.Ecosystem = "auto"
	}
	if a.MaxResults <= 0 {
		a.MaxResults = 80
	}
	if a.MaxChars <= 0 {
		a.MaxChars = 20000
	}
	if a.Package == "" {
		return failResult(start, "package is required"), nil
	}

	res, err := resolveSourceCodePackage(call, a)
	if err != nil {
		return failResult(start, err.Error()), nil
	}

	var output string
	switch a.Action {
	case "resolve":
		output = formatSourceResolve(res)
	case "list":
		output, err = sourceCodeList(res, a.Path)
	case "search":
		output, err = sourceCodeSearch(ctx, res, a.Path, a.Pattern, a.MaxResults, a.MaxChars)
	case "read":
		output, err = sourceCodeRead(res, a.Path, a.StartLine, a.EndLine, a.MaxChars)
	default:
		return failResult(start, "action must be one of resolve, list, search, read"), nil
	}
	if err != nil {
		return failResult(start, err.Error()), nil
	}
	output = capSourceCodeOutput(output, a.MaxChars)

	return ToolResult{
		OK:         true,
		Output:     output,
		Stdout:     output,
		TaintLevel: sourceTaint(res),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func resolveSourceCodePackage(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	ecosystems := []string{a.Ecosystem}
	if a.Ecosystem == "auto" {
		ecosystems = []string{"path", "go", "npm", "pypi", "crates"}
	}
	var errs []string
	for _, ecosystem := range ecosystems {
		var (
			res sourceResolution
			err error
		)
		switch ecosystem {
		case "path":
			res, err = resolvePathSource(call, a)
		case "go":
			res, err = resolveGoSource(call, a)
		case "npm":
			res, err = resolveNPMSource(call, a)
		case "pypi":
			res, err = resolvePyPISource(call, a)
		case "crates":
			res, err = resolveCrateSource(call, a)
		default:
			err = fmt.Errorf("unsupported ecosystem %q", ecosystem)
		}
		if err == nil {
			return res, nil
		}
		errs = append(errs, ecosystem+": "+err.Error())
	}
	return sourceResolution{}, fmt.Errorf("source package %q not found (%s)", a.Package, strings.Join(errs, "; "))
}

func resolvePathSource(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	pkg := a.Package
	if !strings.HasPrefix(pkg, ".") && !strings.HasPrefix(pkg, "/") && !strings.Contains(pkg, string(filepath.Separator)) {
		return sourceResolution{}, fmt.Errorf("not a workspace path")
	}
	root, ok := secureSourcePath(call, pkg)
	if !ok {
		return sourceResolution{}, fmt.Errorf("illegal path outside workspace: %s", pkg)
	}
	info, err := os.Stat(root)
	if err != nil {
		return sourceResolution{}, err
	}
	return sourceResolution{
		Ecosystem:  "path",
		Package:    a.Package,
		Root:       root,
		PackageDir: root,
		Source:     "workspace path",
		IsFile:     !info.IsDir(),
	}, nil
}

func resolveGoSource(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	pkg := strings.Trim(a.Package, "/")
	if pkg == "" {
		return sourceResolution{}, fmt.Errorf("empty Go import path")
	}
	workspace := call.WorkspaceDir
	if workspace == "" {
		return sourceResolution{}, fmt.Errorf("workspace unavailable")
	}

	if mod, ok := readGoModulePath(filepath.Join(workspace, "go.mod")); ok {
		if pkg == mod || strings.HasPrefix(pkg, mod+"/") {
			subdir := strings.TrimPrefix(strings.TrimPrefix(pkg, mod), "/")
			dir := filepath.Join(workspace, filepath.FromSlash(subdir))
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return sourceResolution{
					Ecosystem:  "go",
					Package:    pkg,
					Root:       workspace,
					PackageDir: dir,
					Subdir:     subdir,
					Source:     "current Go module",
				}, nil
			}
		}
	}

	if res, err := resolveGoVendorSource(workspace, pkg, a.Version); err == nil {
		return res, nil
	}

	required := readGoRequiredVersions(filepath.Join(workspace, "go.mod"))
	caches := sourceGoCacheRoots(workspace)
	var best *sourceResolution
	bestScore := -1
	for _, cacheRoot := range caches {
		_ = filepath.WalkDir(cacheRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "cache" || name == ".git" {
				return filepath.SkipDir
			}
			if !strings.Contains(name, "@") {
				return nil
			}
			rel, err := filepath.Rel(cacheRoot, path)
			if err != nil {
				return filepath.SkipDir
			}
			modEsc, version, ok := strings.Cut(filepath.ToSlash(rel), "@")
			if !ok {
				return filepath.SkipDir
			}
			modulePath := unescapeGoModulePath(modEsc)
			if pkg != modulePath && !strings.HasPrefix(pkg, modulePath+"/") {
				return filepath.SkipDir
			}
			if a.Version != "" && a.Version != version {
				return filepath.SkipDir
			}
			subdir := strings.TrimPrefix(strings.TrimPrefix(pkg, modulePath), "/")
			packageDir := filepath.Join(path, filepath.FromSlash(subdir))
			info, statErr := os.Stat(packageDir)
			if statErr != nil || !info.IsDir() {
				return filepath.SkipDir
			}
			score := len(modulePath) * 1000
			if required[modulePath] == version {
				score += 100
			}
			if a.Version != "" && a.Version == version {
				score += 200
			}
			if best == nil || score > bestScore || (score == bestScore && version > best.Version) {
				bestScore = score
				best = &sourceResolution{
					Ecosystem:  "go",
					Package:    pkg,
					Version:    version,
					Root:       path,
					PackageDir: packageDir,
					Subdir:     subdir,
					Source:     "Go module cache",
				}
			}
			return filepath.SkipDir
		})
	}
	if best == nil {
		return sourceResolution{}, fmt.Errorf("not found in current module, vendor, or .gomodcache")
	}
	return *best, nil
}

func resolveGoVendorSource(workspace, pkg, version string) (sourceResolution, error) {
	if version != "" {
		return sourceResolution{}, fmt.Errorf("vendor source has no version selector")
	}
	dir := filepath.Join(workspace, "vendor", filepath.FromSlash(pkg))
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return sourceResolution{}, fmt.Errorf("not vendored")
	}
	return sourceResolution{
		Ecosystem:  "go",
		Package:    pkg,
		Root:       dir,
		PackageDir: dir,
		Source:     "vendor",
	}, nil
}

func resolveNPMSource(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	workspace := call.WorkspaceDir
	if workspace == "" {
		return sourceResolution{}, fmt.Errorf("workspace unavailable")
	}
	name, subdir := splitNPMPackagePath(a.Package)
	if name == "" {
		return sourceResolution{}, fmt.Errorf("invalid npm package")
	}
	for _, root := range []string{
		filepath.Join(workspace, "node_modules", filepath.FromSlash(name)),
		filepath.Join(workspace, ".v100", "source-cache", "npm", filepath.FromSlash(name)),
	} {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			version := readNPMVersion(root)
			if a.Version != "" && version != a.Version {
				continue
			}
			dir := filepath.Join(root, filepath.FromSlash(subdir))
			if subdir != "" {
				if info, err := os.Stat(dir); err != nil || !info.IsDir() {
					continue
				}
			}
			return sourceResolution{
				Ecosystem:  "npm",
				Package:    name,
				Version:    version,
				Root:       root,
				PackageDir: dir,
				Subdir:     subdir,
				Source:     "node_modules",
			}, nil
		}
	}
	return sourceResolution{}, fmt.Errorf("not found in node_modules or .v100/source-cache/npm")
}

func resolvePyPISource(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	workspace := call.WorkspaceDir
	if workspace == "" {
		return sourceResolution{}, fmt.Errorf("workspace unavailable")
	}
	module, subparts := splitPythonPackagePath(a.Package)
	if module == "" {
		return sourceResolution{}, fmt.Errorf("invalid Python package")
	}
	names := pythonNameCandidates(module)
	for _, site := range pythonSitePackageRoots(workspace) {
		for _, name := range names {
			dir := filepath.Join(site, name)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				version := readPythonPackageVersion(site, module)
				if a.Version != "" && version != a.Version {
					continue
				}
				packageDir := dir
				if len(subparts) > 0 {
					packageDir = filepath.Join(dir, filepath.Join(subparts...))
					if info, err := os.Stat(packageDir); err != nil || !info.IsDir() {
						continue
					}
				}
				return sourceResolution{
					Ecosystem:  "pypi",
					Package:    module,
					Version:    version,
					Root:       dir,
					PackageDir: packageDir,
					Subdir:     filepath.ToSlash(filepath.Join(subparts...)),
					Source:     "site-packages",
				}, nil
			}
			file := filepath.Join(site, name+".py")
			if info, err := os.Stat(file); err == nil && !info.IsDir() {
				version := readPythonPackageVersion(site, module)
				if a.Version != "" && version != a.Version {
					continue
				}
				return sourceResolution{
					Ecosystem:  "pypi",
					Package:    module,
					Version:    version,
					Root:       file,
					PackageDir: file,
					Source:     "site-packages",
					IsFile:     true,
				}, nil
			}
		}
	}
	return sourceResolution{}, fmt.Errorf("not found in local virtualenv site-packages")
}

func resolveCrateSource(call ToolCallContext, a sourceCodeArgs) (sourceResolution, error) {
	workspace := call.WorkspaceDir
	if workspace == "" {
		return sourceResolution{}, fmt.Errorf("workspace unavailable")
	}
	crate := strings.TrimSpace(a.Package)
	if crate == "" || strings.Contains(crate, "/") {
		return sourceResolution{}, fmt.Errorf("invalid crate name")
	}
	var best string
	var bestVersion string
	for _, srcRoot := range crateSourceRoots(workspace) {
		entries, err := os.ReadDir(srcRoot)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			prefix := crate + "-"
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			version := strings.TrimPrefix(name, prefix)
			if a.Version != "" && version != a.Version {
				continue
			}
			path := filepath.Join(srcRoot, name)
			if best == "" || version > bestVersion {
				best = path
				bestVersion = version
			}
		}
	}
	if best == "" {
		return sourceResolution{}, fmt.Errorf("not found in local Cargo registry cache")
	}
	return sourceResolution{
		Ecosystem:  "crates",
		Package:    crate,
		Version:    bestVersion,
		Root:       best,
		PackageDir: best,
		Source:     "Cargo registry cache",
	}, nil
}

func sourceCodeList(res sourceResolution, relPath string) (string, error) {
	target, err := sourceTargetPath(res, relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return formatSourcePayload(res, map[string]any{
			"path":  sourceRel(res, target),
			"entry": filepath.Base(target),
			"kind":  "file",
			"size":  info.Size(),
		})
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", err
	}
	rows := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "file"
		name := entry.Name()
		if entry.IsDir() {
			kind = "dir"
			name += "/"
		}
		rows = append(rows, map[string]any{
			"name": name,
			"kind": kind,
			"size": info.Size(),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i]["name"]) < fmt.Sprint(rows[j]["name"])
	})
	return formatSourcePayload(res, map[string]any{
		"path":    sourceRel(res, target),
		"entries": rows,
	})
}

func sourceCodeRead(res sourceResolution, relPath string, startLine, endLine, maxChars int) (string, error) {
	target, err := sourceTargetPath(res, relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path resolves to a directory; use action=list or provide a file path")
	}
	if looksLikeBinary(target) {
		return "", fmt.Errorf("%q appears to be binary or generated", filepath.Base(target))
	}
	content, err := readFileSelection(target, startLine, endLine)
	if err != nil {
		return "", err
	}
	content = capSourceCodeOutput(content, maxChars)
	return formatSourcePayload(res, map[string]any{
		"path":    sourceRel(res, target),
		"content": content,
	})
}

func sourceCodeSearch(ctx context.Context, res sourceResolution, relPath, pattern string, maxResults, maxChars int) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required for action=search")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	target, err := sourceTargetPath(res, relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if d.IsDir() {
				if sourceSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if sourceSkipFile(d.Name()) || looksLikeBinary(path) {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return "", err
		}
	} else {
		files = []string{target}
	}
	sort.Strings(files)

	var matches []string
	for _, file := range files {
		if len(matches) >= maxResults {
			break
		}
		if err := collectSourceMatches(file, res, re, maxResults, &matches); err != nil {
			continue
		}
	}
	out := "(no matches)"
	if len(matches) > 0 {
		out = strings.Join(matches, "\n")
		if len(matches) >= maxResults {
			out += "\n... truncated to max_results"
		}
	}
	out = capSourceCodeOutput(out, maxChars)
	return formatSourcePayload(res, map[string]any{
		"pattern": pattern,
		"path":    sourceRel(res, target),
		"matches": out,
	})
}

func collectSourceMatches(file string, res sourceResolution, re *regexp.Regexp, maxResults int, matches *[]string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !re.MatchString(line) {
			continue
		}
		*matches = append(*matches, fmt.Sprintf("%s:%d:%s", sourceRel(res, file), lineNo, line))
		if len(*matches) >= maxResults {
			return nil
		}
	}
	return scanner.Err()
}

func sourceTargetPath(res sourceResolution, relPath string) (string, error) {
	base := res.PackageDir
	if relPath == "" {
		return base, nil
	}
	if res.IsFile {
		return "", fmt.Errorf("package resolves to a file; omit path or resolve a package directory")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative to package source")
	}
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "." {
		return base, nil
	}
	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(baseForSourceBounds(res), target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes package source")
	}
	return target, nil
}

func baseForSourceBounds(res sourceResolution) string {
	if res.IsFile {
		return filepath.Dir(res.PackageDir)
	}
	return res.PackageDir
}

func formatSourceResolve(res sourceResolution) string {
	return mustJSON(map[string]any{
		"ok":          true,
		"action":      "resolve",
		"ecosystem":   res.Ecosystem,
		"package":     res.Package,
		"version":     res.Version,
		"root":        res.Root,
		"package_dir": res.PackageDir,
		"subdir":      res.Subdir,
		"source":      res.Source,
		"is_file":     res.IsFile,
	})
}

func formatSourcePayload(res sourceResolution, payload map[string]any) (string, error) {
	payload["ok"] = true
	payload["ecosystem"] = res.Ecosystem
	payload["package"] = res.Package
	payload["version"] = res.Version
	payload["root"] = res.Root
	payload["package_dir"] = res.PackageDir
	payload["source"] = res.Source
	return mustJSON(payload), nil
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func capSourceCodeOutput(s string, maxChars int) string {
	if maxChars > 0 && len(s) > maxChars {
		return s[:maxChars] + "\n... truncated to max_chars"
	}
	return s
}

func sourceTaint(res sourceResolution) string {
	if res.Ecosystem == "path" || res.Source == "current Go module" {
		return "workspace_data"
	}
	return "external_dependency_source"
}

func secureSourcePath(call ToolCallContext, path string) (string, bool) {
	if call.Mapper != nil {
		return call.Mapper.SecurePath(path)
	}
	if call.WorkspaceDir == "" {
		return "", false
	}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(call.WorkspaceDir, target)
	}
	rel, err := filepath.Rel(call.WorkspaceDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return target, true
}

func readGoModulePath(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.Fields(line)[1], true
		}
	}
	return "", false
}

func readGoRequiredVersions(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.Split(line, "//")[0])
		if line == "" {
			continue
		}
		if line == "require (" {
			inBlock = true
			continue
		}
		if inBlock && line == ")" {
			inBlock = false
			continue
		}
		if strings.HasPrefix(line, "require ") {
			fields := strings.Fields(strings.TrimPrefix(line, "require "))
			if len(fields) >= 2 {
				out[fields[0]] = fields[1]
			}
			continue
		}
		if inBlock {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				out[fields[0]] = fields[1]
			}
		}
	}
	return out
}

func sourceGoCacheRoots(workspace string) []string {
	roots := []string{
		filepath.Join(workspace, ".gomodcache"),
		filepath.Join(workspace, "go", "pkg", "mod"),
		filepath.Join(workspace, ".v100", "source-cache", "go"),
	}
	if gomodcache := strings.TrimSpace(os.Getenv("GOMODCACHE")); gomodcache != "" {
		roots = append(roots, gomodcache)
	}
	if gopath := strings.TrimSpace(os.Getenv("GOPATH")); gopath != "" {
		for _, entry := range filepath.SplitList(gopath) {
			roots = append(roots, filepath.Join(entry, "pkg", "mod"))
		}
	}
	return existingDirs(roots)
}

func unescapeGoModulePath(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '!' && i+1 < len(s) {
			next := s[i+1]
			if next >= 'a' && next <= 'z' {
				b.WriteByte(next - ('a' - 'A'))
			} else {
				b.WriteByte(next)
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func splitNPMPackagePath(spec string) (string, string) {
	parts := strings.Split(strings.Trim(spec, "/"), "/")
	if len(parts) == 0 {
		return "", ""
	}
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 {
			return "", ""
		}
		name := parts[0] + "/" + parts[1]
		return name, strings.Join(parts[2:], "/")
	}
	return parts[0], strings.Join(parts[1:], "/")
}

func readNPMVersion(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &p)
	return p.Version
}

func readPythonPackageVersion(site, module string) string {
	want := normalizePythonDistName(module)
	entries, err := os.ReadDir(site)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dist-info") {
			continue
		}
		metaPath := filepath.Join(site, entry.Name(), "METADATA")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var name, version string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Name:") {
				name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			}
			if strings.HasPrefix(line, "Version:") {
				version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
			}
		}
		if normalizePythonDistName(name) == want {
			return version
		}
	}
	return ""
}

func normalizePythonDistName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' {
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
	}
	return strings.Trim(b.String(), "-")
}

func splitPythonPackagePath(spec string) (string, []string) {
	parts := strings.Split(strings.Trim(spec, "."), ".")
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

func pythonNameCandidates(name string) []string {
	name = strings.TrimSpace(name)
	norm := strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	return uniqueStrings([]string{name, norm, strings.ReplaceAll(norm, "_", "-")})
}

func pythonSitePackageRoots(workspace string) []string {
	var out []string
	for _, base := range []string{
		filepath.Join(workspace, ".venv"),
		filepath.Join(workspace, "venv"),
		filepath.Join(workspace, ".tox"),
		filepath.Join(workspace, ".v100", "source-cache", "pypi"),
	} {
		_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if d.Name() == "site-packages" {
				out = append(out, path)
				return filepath.SkipDir
			}
			return nil
		})
	}
	return uniqueExistingDirs(out)
}

func crateSourceRoots(workspace string) []string {
	var out []string
	roots := []string{
		filepath.Join(workspace, ".cargo", "registry", "src"),
		filepath.Join(workspace, ".v100", "source-cache", "crates"),
	}
	if cargoHome := strings.TrimSpace(os.Getenv("CARGO_HOME")); cargoHome != "" {
		roots = append(roots, filepath.Join(cargoHome, "registry", "src"))
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				out = append(out, filepath.Join(root, entry.Name()))
			}
		}
	}
	return uniqueExistingDirs(out)
}

func existingDirs(paths []string) []string {
	return uniqueExistingDirs(paths)
}

func uniqueExistingDirs(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func sourceRel(res sourceResolution, path string) string {
	base := res.PackageDir
	if res.IsFile {
		base = filepath.Dir(res.PackageDir)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	if rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}

func sourceSkipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "dist", "build", "target", "__pycache__":
		return true
	default:
		return false
	}
}

func sourceSkipFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".zip", ".tar", ".gz", ".so", ".a", ".o", ".wasm":
		return true
	default:
		return false
	}
}

var _ Tool = (*sourceCodeTool)(nil)
