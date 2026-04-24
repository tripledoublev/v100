package i18n

import (
	"testing"
)

func TestT_English(t *testing.T) {
	t.Setenv("V100_LANG", "")
	if got := T("status_idle"); got != "idle" {
		t.Fatalf("en status_idle = %q, want %q", got, "idle")
	}
	if got := T("status_ready"); got != "ready and waiting" {
		t.Fatalf("en status_ready = %q, want %q", got, "ready and waiting")
	}
}

func TestT_French(t *testing.T) {
	t.Setenv("V100_LANG", "fr")
	if got := T("status_idle"); got != "inactif" {
		t.Fatalf("fr status_idle = %q, want %q", got, "inactif")
	}
	if got := T("status_thinking"); got != "réflexion" {
		t.Fatalf("fr status_thinking = %q, want %q", got, "réflexion")
	}
}

func TestT_FallsBackToEnglish(t *testing.T) {
	t.Setenv("V100_LANG", "fr")
	if got := T("missing_key"); got != "missing_key" {
		t.Fatalf("missing key should return key itself, got %q", got)
	}
}

func TestT_LocaleNormalization(t *testing.T) {
	t.Setenv("V100_LANG", "fr-CA")
	if got := T("status_idle"); got != "inactif" {
		t.Fatalf("fr-CA should normalize to fr, got %q", got)
	}
}

func TestT_LocaleNormalizationUnderscore(t *testing.T) {
	t.Setenv("V100_LANG", "fr_CA")
	if got := T("status_idle"); got != "inactif" {
		t.Fatalf("fr_CA should normalize to fr, got %q", got)
	}
}

func TestStatusMode_String(t *testing.T) {
	cases := []struct {
		mode StatusMode
		want string
	}{
		{StatusIdle, "idle"},
		{StatusThinking, "thinking"},
		{StatusTooling, "tooling"},
		{StatusError, "error"},
		{StatusDownloading, "downloading"},
	}
	for _, c := range cases {
		if got := c.mode.String(); got != c.want {
			t.Errorf("StatusMode(%d).String() = %q, want %q", c.mode, got, c.want)
		}
	}
}

func TestStatusMode_Locale_French(t *testing.T) {
	t.Setenv("V100_LANG", "fr")
	if got := StatusIdle.Locale(); got != "inactif" {
		t.Fatalf("fr StatusIdle.Locale() = %q, want %q", got, "inactif")
	}
	if got := StatusThinking.Locale(); got != "réflexion" {
		t.Fatalf("fr StatusThinking.Locale() = %q, want %q", got, "réflexion")
	}
}

func TestStatusMode_LocaleUnknown(t *testing.T) {
	if got := StatusMode(999).Locale(); got != "unknown" {
		t.Fatalf("unknown StatusMode.Locale() = %q, want %q", got, "unknown")
	}
}
