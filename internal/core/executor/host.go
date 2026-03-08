package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HostExecutor creates host-based sessions.
type HostExecutor struct {
	BaseDir string // root directory for runs (e.g. "runs")
}

func NewHostExecutor(baseDir string) *HostExecutor {
	return &HostExecutor{BaseDir: baseDir}
}

func (e *HostExecutor) NewSession(runID, sourceWorkspace string) (Session, error) {
	return &HostSession{
		runID:           runID,
		sourceWorkspace: sourceWorkspace,
		sandboxDir:      filepath.Join(e.BaseDir, runID, "workspace"),
	}, nil
}

// HostSession runs commands directly on the host in a sandbox directory.
type HostSession struct {
	runID           string
	sourceWorkspace string
	sandboxDir      string
}

func (s *HostSession) ID() string   { return s.runID }
func (s *HostSession) Type() string { return "host" }

func (s *HostSession) Start(ctx context.Context) error {
	// 1. Create sandbox directory
	if err := os.MkdirAll(s.sandboxDir, 0755); err != nil {
		return fmt.Errorf("host session: mkdir: %w", err)
	}

	// 2. Materialize workspace (copy source to sandbox)
	// For research grade, we'll use a simple copy for now.
	// Future: use reflink or fast sync.
	if err := copyDir(s.sourceWorkspace, s.sandboxDir); err != nil {
		return fmt.Errorf("host session: materialize: %w", err)
	}

	return nil
}

func (s *HostSession) Close() error {
	// Optionally clean up if policy says so. For now keep for observability.
	return nil
}

func (s *HostSession) Run(ctx context.Context, req RunRequest) (Result, error) {
	fullDir := filepath.Join(s.sandboxDir, req.Dir)
	
	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	cmd.Dir = fullDir
	cmd.Env = append(os.Environ(), req.Env...)

	var stdout, stderr bytes.Buffer
	var stdoutW io.Writer = &stdout
	var stderrW io.Writer = &stderr

	if req.StdoutWriter != nil {
		stdoutW = io.MultiWriter(stdoutW, req.StdoutWriter)
	}
	if req.StderrWriter != nil {
		stderrW = io.MultiWriter(stderrW, req.StderrWriter)
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, err
		}
	}

	return Result{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      err,
	}, nil
}

func (s *HostSession) Workspace() string {
	return s.sandboxDir
}

// copyDir recursively copies a directory tree.
func copyDir(src string, dst string) error {
	// Skip the runs directory if it's inside the source (e.g. running in project root)
	srcAbs, _ := filepath.Abs(src)
	dstAbs, _ := filepath.Abs(dst)

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, rel)
		targetAbs, _ := filepath.Abs(targetPath)

		// Prevent infinite recursion if runs/ is inside src
		if strings.HasPrefix(targetAbs, srcAbs) && info.IsDir() && info.Name() == "runs" {
			return filepath.SkipDir
		}
		// Also don't copy the sandbox into itself if it happens to be nested
		if targetAbs == dstAbs {
			return nil
		}

		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		// Copy file
		return copyFile(path, targetPath)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, si.Mode())
}
