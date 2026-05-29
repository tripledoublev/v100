package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var ErrSecretUnavailable = errors.New("secret unavailable")

type SecretValue struct {
	Value  string
	Source string
}

type SecretManager interface {
	Name() string
	Get(ctx context.Context, key string) (string, error)
}

var defaultSecretManagers = func() []SecretManager {
	return []SecretManager{
		OnePasswordManager{},
		PassManager{},
		SystemKeyringManager{},
	}
}

var plaintextFallbackWarningWriter io.Writer = os.Stderr

func warnPlaintextFallback(kind, path string) {
	if plaintextFallbackWarningWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(plaintextFallbackWarningWriter, "auth: warning: using plaintext %s from %s; prefer env vars or 1Password/pass/system keyring\n", kind, path)
}

var runSecretCommand = func(ctx context.Context, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", fmt.Errorf("%w: %s not found", ErrSecretUnavailable, name)
	}
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrSecretUnavailable, name, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("%w: %s returned an empty value", ErrSecretUnavailable, name)
	}
	return value, nil
}

func ResolveSecret(ctx context.Context, key string, envNames ...string) (SecretValue, error) {
	for _, name := range envNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return SecretValue{Value: value, Source: "env:" + name}, nil
		}
	}
	return LookupSecret(ctx, key, defaultSecretManagers())
}

func LookupSecret(ctx context.Context, key string, managers []SecretManager) (SecretValue, error) {
	var tried []string
	for _, manager := range managers {
		if manager == nil {
			continue
		}
		value, err := manager.Get(ctx, key)
		if err == nil && strings.TrimSpace(value) != "" {
			return SecretValue{Value: strings.TrimSpace(value), Source: manager.Name()}, nil
		}
		tried = append(tried, manager.Name())
	}
	if len(tried) == 0 {
		return SecretValue{}, fmt.Errorf("%w: no secret managers configured", ErrSecretUnavailable)
	}
	return SecretValue{}, fmt.Errorf("%w: %s not found via %s", ErrSecretUnavailable, key, strings.Join(tried, ", "))
}

type OnePasswordManager struct{}

func (OnePasswordManager) Name() string { return "1password" }

func (OnePasswordManager) Get(ctx context.Context, key string) (string, error) {
	prefix := strings.TrimRight(strings.TrimSpace(os.Getenv("V100_1PASSWORD_PREFIX")), "/")
	if prefix == "" {
		prefix = "op://Private/v100"
	}
	return runSecretCommand(ctx, "op", "read", prefix+"/"+key)
}

type PassManager struct{}

func (PassManager) Name() string { return "pass" }

func (PassManager) Get(ctx context.Context, key string) (string, error) {
	prefix := strings.Trim(strings.TrimSpace(os.Getenv("V100_PASS_PREFIX")), "/")
	if prefix == "" {
		prefix = "v100"
	}
	return runSecretCommand(ctx, "pass", "show", prefix+"/"+key)
}

type SystemKeyringManager struct{}

func (SystemKeyringManager) Name() string { return "system-keyring" }

func (SystemKeyringManager) Get(ctx context.Context, key string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return runSecretCommand(ctx, "security", "find-generic-password", "-s", "v100", "-a", key, "-w")
	case "linux":
		return runSecretCommand(ctx, "secret-tool", "lookup", "service", "v100", "key", key)
	default:
		return "", fmt.Errorf("%w: system keyring unsupported on %s", ErrSecretUnavailable, runtime.GOOS)
	}
}
