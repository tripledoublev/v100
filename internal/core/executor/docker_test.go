package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
		"-e GOTELEMETRY=off",
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

func TestDockerSessionWriteSeccompRequiresPath(t *testing.T) {
	session := &DockerSession{}
	if err := session.writeSeccompProfile(); err == nil {
		t.Fatal("writeSeccompProfile returned nil, want error")
	}
}

func TestDockerSessionIdentityAndContainerName(t *testing.T) {
	session := &DockerSession{runID: "run-1"}
	if session.ID() != "run-1" {
		t.Fatalf("ID() = %q, want run-1", session.ID())
	}
	if session.Type() != "docker" {
		t.Fatalf("Type() = %q, want docker", session.Type())
	}
	if got := dockerContainerName("a/b:c d"); got != "v100-a-b-c-d" {
		t.Fatalf("dockerContainerName() = %q, want v100-a-b-c-d", got)
	}
	if got := dockerContainerName(" "); got != "v100-run" {
		t.Fatalf("dockerContainerName(blank) = %q, want v100-run", got)
	}
}

func TestDockerSessionRunUsesProcessRunner(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "docker.log")
	installFakeDocker(t, fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
cat >/dev/null
echo docker-stdout
echo docker-stderr >&2
exit 7
`, strconv.Quote(logPath)))

	session := &DockerSession{containerName: "v100-run-1"}
	res, err := session.Run(context.Background(), RunRequest{
		Command: "printf",
		Args:    []string{"ok"},
		Dir:     "subdir",
		Env:     []string{"A=B"},
		Stdin:   "input",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", res.ExitCode)
	}
	if res.Err == nil {
		t.Fatal("Result.Err is nil, want exit error")
	}
	if !strings.Contains(res.Stdout, "docker-stdout") || !strings.Contains(res.Stderr, "docker-stderr") {
		t.Fatalf("unexpected output stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"exec -i", "-w /workspace/subdir", "-e A=B", "v100-run-1 printf ok"} {
		if !strings.Contains(string(logged), want) {
			t.Fatalf("fake docker log missing %q in:\n%s", want, string(logged))
		}
	}
}

func TestDockerSessionRunKillsContainerOnCancellation(t *testing.T) {
	restoreGrace := setProcessKillGraceForTest(10 * time.Millisecond)
	defer restoreGrace()

	logPath := filepath.Join(t.TempDir(), "docker.log")
	installFakeDocker(t, fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
if [ "$1" = "exec" ]; then
  sleep 10
fi
exit 0
`, strconv.Quote(logPath)))

	session := &DockerSession{containerName: "v100-run-1"}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	res, err := session.Run(ctx, RunRequest{
		Command: "sleep",
		Args:    []string{"10"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Err == nil || res.ExitCode == 0 {
		t.Fatalf("Run result = exit %d err %v, want canceled docker exec", res.ExitCode, res.Err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"exec -w /workspace",
		"kill --signal TERM v100-run-1",
		"kill --signal KILL v100-run-1",
	} {
		if !strings.Contains(string(logged), want) {
			t.Fatalf("fake docker log missing %q in:\n%s", want, string(logged))
		}
	}
}

func TestDockerSessionStartAndCloseUseProcessRunner(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "docker.log")
	installFakeDocker(t, fmt.Sprintf(`#!/bin/sh
echo "$@" >> %s
exit 0
`, strconv.Quote(logPath)))

	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "marker.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	session := &DockerSession{
		runID:           "run-1",
		sourceWorkspace: sourceDir,
		sandboxDir:      filepath.Join(runDir, "workspace"),
		containerName:   "v100-run-1",
		seccompPath:     filepath.Join(runDir, "docker-seccomp.json"),
		image:           "example/v100:latest",
		networkMode:     "none",
	}
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session.sandboxDir, "marker.txt")); err != nil {
		t.Fatalf("workspace missing marker: %v", err)
	}
	if _, err := os.Stat(session.seccompPath); err != nil {
		t.Fatalf("seccomp profile missing: %v", err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rm -f v100-run-1", "run -d --rm", "--name v100-run-1"} {
		if !strings.Contains(string(logged), want) {
			t.Fatalf("fake docker log missing %q in:\n%s", want, string(logged))
		}
	}
}

func TestDockerSessionStartRequiresImage(t *testing.T) {
	session := &DockerSession{}
	if err := session.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil, want missing image error")
	}
}

func TestDockerSessionCloseReportsFailure(t *testing.T) {
	installFakeDocker(t, `#!/bin/sh
echo bad >&2
exit 2
`)
	session := &DockerSession{containerName: "v100-run-1"}
	if err := session.Close(); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("Close error = %v, want docker failure", err)
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

func installFakeDocker(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
