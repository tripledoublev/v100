package ui

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestDigestCausePrefersRunError(t *testing.T) {
	d := core.RunDigest{
		EndReason: "error",
		RunErrors: []string{"goroutine 7 [running]:\nError: provider stream: 429 quota exceeded\nstack traceback:"},
		ToolFailures: []core.DigestToolFailure{{
			Name:   "sh",
			Output: "command failed",
		}},
	}

	got := DigestCause(d)
	if got != "provider stream: 429 quota exceeded" {
		t.Fatalf("DigestCause() = %q, want %q", got, "provider stream: 429 quota exceeded")
	}
}

func TestDigestCauseFallsBackToToolFailure(t *testing.T) {
	d := core.RunDigest{
		EndReason: "budget_exceeded",
		ToolFailures: []core.DigestToolFailure{{
			Name:   "project_search",
			Output: "\nerror: rg: regex parse error:\n    (foo\n    ^\n",
		}},
	}

	got := DigestCause(d)
	if got != "project_search: rg: regex parse error:" {
		t.Fatalf("DigestCause() = %q", got)
	}
}

func TestFormatDigestStyledIncludesCauseAndCompactsDetails(t *testing.T) {
	out := FormatDigestStyled(core.RunDigest{
		RunID:      "run-1",
		EndReason:  "error",
		RunErrors:  []string{"Error: provider stream: connection reset by peer\nstack traceback:\nframe 1"},
		TotalSteps: 2,
	})

	if !strings.Contains(out, "Cause:") {
		t.Fatalf("expected Cause line in digest:\n%s", out)
	}
	if !strings.Contains(out, "provider stream: connection reset by peer") {
		t.Fatalf("expected compact cause in digest:\n%s", out)
	}
	if strings.Contains(out, "stack traceback") {
		t.Fatalf("expected stack trace to be compacted out of digest:\n%s", out)
	}
}
