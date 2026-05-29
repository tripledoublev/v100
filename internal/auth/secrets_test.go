package auth

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestResolveSecretChecksEnvFirst(t *testing.T) {
	t.Setenv("V100_TEST_SECRET", " env-value ")
	withSecretManagers(t, fakeSecretManager{
		values: map[string]string{"test_secret": "manager-value"},
	})

	secret, err := ResolveSecret(context.Background(), "test_secret", "V100_TEST_SECRET")
	if err != nil {
		t.Fatalf("ResolveSecret() error = %v", err)
	}
	if secret.Value != "env-value" || secret.Source != "env:V100_TEST_SECRET" {
		t.Fatalf("secret = %+v", secret)
	}
}

func TestLookupSecretUsesManagersAndReportsFailure(t *testing.T) {
	secret, err := LookupSecret(context.Background(), "wanted", []SecretManager{
		fakeSecretManager{name: "empty", values: map[string]string{"other": "value"}},
		fakeSecretManager{name: "hit", values: map[string]string{"wanted": "manager-value"}},
	})
	if err != nil {
		t.Fatalf("LookupSecret() error = %v", err)
	}
	if secret.Value != "manager-value" || secret.Source != "hit" {
		t.Fatalf("secret = %+v", secret)
	}

	_, err = LookupSecret(context.Background(), "missing", []SecretManager{
		fakeSecretManager{name: "one", err: errors.New("nope")},
		fakeSecretManager{name: "two", err: errors.New("nope")},
	})
	if err == nil || !strings.Contains(err.Error(), "one, two") {
		t.Fatalf("missing error = %v", err)
	}

	_, err = LookupSecret(context.Background(), "missing", nil)
	if !errors.Is(err, ErrSecretUnavailable) || !strings.Contains(err.Error(), "no secret managers configured") {
		t.Fatalf("nil manager error = %v", err)
	}
}

func TestSecretManagerCommandAdapters(t *testing.T) {
	oldRun := runSecretCommand
	defer func() { runSecretCommand = oldRun }()
	var calls []string
	runSecretCommand = func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return " command-value ", nil
	}

	t.Setenv("V100_1PASSWORD_PREFIX", "op://Vault/v100")
	if (OnePasswordManager{}).Name() != "1password" {
		t.Fatal("unexpected 1Password manager name")
	}
	value, err := OnePasswordManager{}.Get(context.Background(), "oauth_codex_client_id")
	if err != nil || value != " command-value " {
		t.Fatalf("1Password value=%q err=%v", value, err)
	}
	if calls[0] != "op read op://Vault/v100/oauth_codex_client_id" {
		t.Fatalf("1Password call = %q", calls[0])
	}

	t.Setenv("V100_PASS_PREFIX", "team/v100")
	if (PassManager{}).Name() != "pass" {
		t.Fatal("unexpected pass manager name")
	}
	value, err = PassManager{}.Get(context.Background(), "oauth_gemini_client_secret")
	if err != nil || value != " command-value " {
		t.Fatalf("pass value=%q err=%v", value, err)
	}
	if calls[1] != "pass show team/v100/oauth_gemini_client_secret" {
		t.Fatalf("pass call = %q", calls[1])
	}

	if (SystemKeyringManager{}).Name() != "system-keyring" {
		t.Fatal("unexpected system keyring manager name")
	}
	_, _ = SystemKeyringManager{}.Get(context.Background(), "provider_anthropic_api_key")
	switch runtime.GOOS {
	case "darwin":
		if !strings.HasPrefix(calls[2], "security find-generic-password -s v100 -a provider_anthropic_api_key -w") {
			t.Fatalf("security call = %q", calls[2])
		}
	case "linux":
		if calls[2] != "secret-tool lookup service v100 key provider_anthropic_api_key" {
			t.Fatalf("secret-tool call = %q", calls[2])
		}
	default:
		if len(calls) != 2 {
			t.Fatalf("unexpected system keyring command on %s: %v", runtime.GOOS, calls)
		}
	}
}
