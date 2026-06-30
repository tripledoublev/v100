package gateway

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/acp"
)

const (
	defaultACPInitializeTimeout = 10 * time.Second
	defaultACPShutdownTimeout   = 5 * time.Second
)

// ACPProcessOptions controls the ACP child process used by gateways.
type ACPProcessOptions struct {
	ConfigPath        string
	Provider          string
	ClientInfoName    string
	ClientInfoVersion string
	ExitReason        string
	InitializeTimeout time.Duration
	ShutdownTimeout   time.Duration
	OnNotification    func(acp.Notification)
}

// ACPProcess is a running ACP child and client pair.
type ACPProcess struct {
	Client *acp.Client
	Stop   func() error
}

type acpRunner interface {
	stdinPipe() (io.WriteCloser, error)
	stdoutPipe() (io.ReadCloser, error)
	start() error
	kill() error
	wait() error
}

type execRunner struct {
	cmd *exec.Cmd
}

func (r *execRunner) stdinPipe() (io.WriteCloser, error) { return r.cmd.StdinPipe() }
func (r *execRunner) stdoutPipe() (io.ReadCloser, error) { return r.cmd.StdoutPipe() }
func (r *execRunner) start() error                       { return r.cmd.Start() }
func (r *execRunner) kill() error {
	if r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Kill()
}
func (r *execRunner) wait() error { return r.cmd.Wait() }

var newACPRunner = func(ctx context.Context, exe string, args []string, stderr io.Writer) acpRunner {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Stderr = stderr
	return &execRunner{cmd: cmd}
}

// StartACPProcess starts and initializes a v100 acp child process.
func StartACPProcess(ctx context.Context, opts ACPProcessOptions) (*ACPProcess, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return startACPProcessWithExecutable(ctx, exe, opts)
}

func startACPProcessWithExecutable(ctx context.Context, exe string, opts ACPProcessOptions) (*ACPProcess, error) {
	args := ACPProcessArgs(opts.ConfigPath, opts.Provider)
	child := newACPRunner(ctx, exe, args, os.Stderr)
	stdin, err := child.stdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp stdin: %w", err)
	}
	stdout, err := child.stdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("acp stdout: %w", err)
	}
	if err := child.start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start acp process: %w", err)
	}

	client := acp.NewClient(stdout, stdin, opts.OnNotification)
	client.StartLaunch()
	initTimeout := opts.InitializeTimeout
	if initTimeout <= 0 {
		initTimeout = defaultACPInitializeTimeout
	}
	initCtx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	if err := InitializeACP(initCtx, client, opts); err != nil {
		_ = child.kill()
		_ = child.wait()
		return nil, fmt.Errorf("initialize acp server: %w", err)
	}

	stop := func() error {
		if ctx.Err() == nil {
			shutdownTimeout := opts.ShutdownTimeout
			if shutdownTimeout <= 0 {
				shutdownTimeout = defaultACPShutdownTimeout
			}
			finalizeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			_ = client.Call(finalizeCtx, acp.MethodFinalize, acp.FinalizeParams{Reason: acpExitReason(opts)}, nil)
			cancel()
		}
		_ = child.kill()
		return child.wait()
	}

	return &ACPProcess{Client: client, Stop: stop}, nil
}

// ACPProcessArgs builds the child process argv after the executable.
func ACPProcessArgs(cfgPath, provider string) []string {
	args := []string{"acp"}
	if strings.TrimSpace(cfgPath) != "" {
		args = append(args, "--config", cfgPath)
	}
	if strings.TrimSpace(provider) != "" {
		args = append(args, "--provider", provider)
	}
	return args
}

// InitializeACP sends the ACP initialize request used by gateway child clients.
func InitializeACP(ctx context.Context, cli ACPClient, opts ACPProcessOptions) error {
	var initRes acp.InitializeResult
	return cli.Call(ctx, acp.MethodInitialize, acp.InitializeParams{
		ProtocolVersion: acp.ProtocolVersion,
		ClientInfo: acp.ClientInfo{
			Name:    acpClientInfoName(opts),
			Version: acpClientInfoVersion(opts),
		},
		ClientCapabilities: acp.ClientCapabilities{},
	}, &initRes)
}

func acpClientInfoName(opts ACPProcessOptions) string {
	if strings.TrimSpace(opts.ClientInfoName) != "" {
		return strings.TrimSpace(opts.ClientInfoName)
	}
	return "v100-gateway"
}

func acpClientInfoVersion(opts ACPProcessOptions) string {
	if strings.TrimSpace(opts.ClientInfoVersion) != "" {
		return strings.TrimSpace(opts.ClientInfoVersion)
	}
	return "dev"
}

func acpExitReason(opts ACPProcessOptions) string {
	if strings.TrimSpace(opts.ExitReason) != "" {
		return strings.TrimSpace(opts.ExitReason)
	}
	return "gateway_exit"
}
