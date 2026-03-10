package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/config"
)

func TestDockerNetworkMode(t *testing.T) {
	tests := []struct {
		tier string
		want string
	}{
		{tier: "", want: "none"},
		{tier: "off", want: "none"},
		{tier: "research", want: "bridge"},
		{tier: "open", want: "bridge"},
	}
	for _, tt := range tests {
		if got := dockerNetworkMode(tt.tier); got != tt.want {
			t.Fatalf("dockerNetworkMode(%q) = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestDockerSessionStartArgs(t *testing.T) {
	session := &DockerSession{
		runID:         "run-1",
		sandboxDir:    "/tmp/v100/run-1/workspace",
		containerName: dockerContainerName("run-1"),
		seccompPath:   "/tmp/v100/run-1/docker-seccomp.json",
		image:         "example/v100:latest",
		networkMode:   "none",
		memoryMB:      768,
		cpus:          1.5,
	}

	args := session.startArgs()
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run -d --rm",
		"--name v100-run-1",
		"--label io.v100.managed=true",
		"--label io.v100.run_id=run-1",
		"--workdir /workspace",
		"--mount type=bind,src=/tmp/v100/run-1/workspace,dst=/workspace",
		"--network none",
		"--pids-limit 64",
		"--memory 768m",
		"--cpus 1.50",
		"--cap-drop ALL",
		"--security-opt no-new-privileges",
		"--security-opt seccomp=/tmp/v100/run-1/docker-seccomp.json",
		"example/v100:latest sh -lc",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker start args missing %q:\n%s", want, joined)
		}
	}
}

func TestDockerSessionExecArgs(t *testing.T) {
	session := &DockerSession{containerName: "v100-run-1"}
	args := session.execArgs(RunRequest{
		Command: "git",
		Args:    []string{"status"},
		Dir:     "subdir",
		Env:     []string{"A=B"},
		Stdin:   "input",
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"exec -i",
		"-w /workspace/subdir",
		"-e HOME=/workspace",
		"-e GOCACHE=/workspace/.cache/go-build",
		"-e GOMODCACHE=/workspace/.cache/go-mod",
		"-e GOPATH=/workspace/.cache/go",
		"-e A=B",
		"v100-run-1 git status",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker exec args missing %q:\n%s", want, joined)
		}
	}
}

func TestDockerSessionWritesSeccompProfile(t *testing.T) {
	session := &DockerSession{
		seccompPath: filepath.Join(t.TempDir(), "run-1", "docker-seccomp.json"),
	}
	if err := session.writeSeccompProfile(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(session.seccompPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"mount"`) || !strings.Contains(string(b), `"ptrace"`) {
		t.Fatalf("seccomp profile missing expected denied syscalls: %s", string(b))
	}
}

func TestFactoryReturnsDockerExecutor(t *testing.T) {
	execFactory, err := NewExecutor(config.SandboxConfig{
		Enabled: true,
		Backend: "docker",
		Image:   "example/v100:latest",
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := execFactory.(*DockerExecutor); !ok {
		t.Fatalf("factory returned %T, want *DockerExecutor", execFactory)
	}
}

func TestDockerExecutorSessionWorkspace(t *testing.T) {
	baseDir := t.TempDir()
	execFactory := NewDockerExecutor(config.SandboxConfig{
		Image: "example/v100:latest",
	}, baseDir)
	session, err := execFactory.NewSession("run-1", "/tmp/source")
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Workspace(); got != filepath.Join(baseDir, "run-1", "workspace") {
		t.Fatalf("Workspace() = %q, want %q", got, filepath.Join(baseDir, "run-1", "workspace"))
	}
}

func TestNewDockerExecutorNormalizesBaseDirToAbsolute(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	execFactory := NewDockerExecutor(config.SandboxConfig{
		Image: "example/v100:latest",
	}, "runs")
	if !filepath.IsAbs(execFactory.BaseDir) {
		t.Fatalf("BaseDir = %q, want absolute path", execFactory.BaseDir)
	}
	if execFactory.BaseDir != filepath.Join(cwd, "runs") {
		t.Fatalf("BaseDir = %q, want %q", execFactory.BaseDir, filepath.Join(cwd, "runs"))
	}
}
