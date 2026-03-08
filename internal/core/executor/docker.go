package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

const (
	dockerManagedLabel   = "io.v100.managed=true"
	dockerRunIDLabel     = "io.v100.run_id"
	dockerCommandTimeout = 5 * time.Second
)

const dockerSeccompProfile = `{
  "defaultAction": "SCMP_ACT_ALLOW",
  "architectures": [
    "SCMP_ARCH_X86_64",
    "SCMP_ARCH_X86",
    "SCMP_ARCH_X32",
    "SCMP_ARCH_AARCH64",
    "SCMP_ARCH_ARM"
  ],
  "syscalls": [
    {
      "names": [
        "mount",
        "umount2",
        "ptrace",
        "kexec_load",
        "open_by_handle_at",
        "init_module",
        "finit_module",
        "delete_module"
      ],
      "action": "SCMP_ACT_ERRNO"
    }
  ]
}`

// DockerExecutor creates Docker-backed sessions.
type DockerExecutor struct {
	BaseDir string
	Cfg     config.SandboxConfig
}

func NewDockerExecutor(cfg config.SandboxConfig, baseDir string) *DockerExecutor {
	return &DockerExecutor{BaseDir: baseDir, Cfg: cfg}
}

func (e *DockerExecutor) NewSession(runID, sourceWorkspace string) (Session, error) {
	return &DockerSession{
		runID:           runID,
		sourceWorkspace: sourceWorkspace,
		sandboxDir:      filepath.Join(e.BaseDir, runID, "workspace"),
		containerName:   dockerContainerName(runID),
		seccompPath:     filepath.Join(e.BaseDir, runID, "docker-seccomp.json"),
		image:           strings.TrimSpace(e.Cfg.Image),
		networkMode:     dockerNetworkMode(e.Cfg.NetworkTier),
		memoryMB:        e.Cfg.MemoryMB,
		cpus:            e.Cfg.CPUs,
	}, nil
}

// DockerSession runs commands in a long-lived Docker container.
type DockerSession struct {
	runID           string
	sourceWorkspace string
	sandboxDir      string
	containerName   string
	seccompPath     string
	image           string
	networkMode     string
	memoryMB        int
	cpus            float64
}

func (s *DockerSession) ID() string   { return s.runID }
func (s *DockerSession) Type() string { return "docker" }

func (s *DockerSession) Start(ctx context.Context) error {
	if strings.TrimSpace(s.image) == "" {
		return fmt.Errorf("docker session: image is required")
	}
	if err := os.MkdirAll(s.sandboxDir, 0o755); err != nil {
		return fmt.Errorf("docker session: mkdir: %w", err)
	}
	if err := copyDir(s.sourceWorkspace, s.sandboxDir); err != nil {
		return fmt.Errorf("docker session: materialize: %w", err)
	}
	if err := s.writeSeccompProfile(); err != nil {
		return err
	}

	_, _ = s.runDocker(ctx, nil, nil, nil, "rm", "-f", s.containerName)

	res, err := s.runDocker(ctx, nil, nil, nil, s.startArgs()...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("docker session: start failed: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

func (s *DockerSession) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), dockerCommandTimeout)
	defer cancel()
	res, err := s.runDocker(ctx, nil, nil, nil, "rm", "-f", s.containerName)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && !strings.Contains(res.Stderr+res.Stdout, "No such container") {
		return fmt.Errorf("docker session: close failed: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

func (s *DockerSession) Run(ctx context.Context, req RunRequest) (Result, error) {
	var stdin io.Reader
	if req.Stdin != "" {
		stdin = strings.NewReader(req.Stdin)
	}
	return s.runDocker(ctx, stdin, req.StdoutWriter, req.StderrWriter, s.execArgs(req)...)
}

func (s *DockerSession) Workspace() string { return s.sandboxDir }

func (s *DockerSession) startArgs() []string {
	args := []string{
		"run", "-d", "--rm",
		"--name", s.containerName,
		"--label", dockerManagedLabel,
		"--label", dockerRunIDLabel + "=" + s.runID,
		"--workdir", "/workspace",
		"--mount", fmt.Sprintf("type=bind,src=%s,dst=/workspace", s.sandboxDir),
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--security-opt", "seccomp=" + s.seccompPath,
		"--pids-limit", "64",
		"--network", s.networkMode,
	}
	if s.memoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", s.memoryMB))
	}
	if s.cpus > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", s.cpus))
	}
	args = append(args,
		s.image,
		"sh", "-lc", "trap 'exit 0' TERM INT; while :; do sleep 3600; done",
	)
	return args
}

func (s *DockerSession) execArgs(req RunRequest) []string {
	workdir := "/workspace"
	if dir := strings.TrimSpace(req.Dir); dir != "" && dir != "." {
		workdir = path.Join("/workspace", filepath.ToSlash(dir))
	}
	args := []string{"exec"}
	if req.Stdin != "" {
		args = append(args, "-i")
	}
	args = append(args, "-w", workdir)
	for _, env := range req.Env {
		if strings.TrimSpace(env) == "" {
			continue
		}
		args = append(args, "-e", env)
	}
	args = append(args, s.containerName, req.Command)
	args = append(args, req.Args...)
	return args
}

func (s *DockerSession) runDocker(ctx context.Context, stdin io.Reader, stdoutWriter, stderrWriter io.Writer, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stdout, stderr bytes.Buffer
	var stdoutW io.Writer = &stdout
	var stderrW io.Writer = &stderr
	if stdoutWriter != nil {
		stdoutW = io.MultiWriter(stdoutW, stdoutWriter)
	}
	if stderrWriter != nil {
		stderrW = io.MultiWriter(stderrW, stderrWriter)
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

func dockerContainerName(runID string) string {
	runID = strings.TrimSpace(runID)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	runID = replacer.Replace(runID)
	if runID == "" {
		runID = "run"
	}
	return "v100-" + runID
}

func dockerNetworkMode(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "", "off":
		return "none"
	default:
		return "bridge"
	}
}

func (s *DockerSession) writeSeccompProfile() error {
	if strings.TrimSpace(s.seccompPath) == "" {
		return fmt.Errorf("docker session: seccomp path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.seccompPath), 0o755); err != nil {
		return fmt.Errorf("docker session: mkdir seccomp dir: %w", err)
	}
	if err := os.WriteFile(s.seccompPath, []byte(dockerSeccompProfile), 0o644); err != nil {
		return fmt.Errorf("docker session: write seccomp profile: %w", err)
	}
	return nil
}
