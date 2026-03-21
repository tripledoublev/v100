package ui

import (
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
)

// FormatDigestStyled returns a colorized failure digest.
func FormatDigestStyled(d core.RunDigest) string {
	var b strings.Builder

	header := stylePrimary.Render("Failure Digest")
	fmt.Fprintf(&b, "%s\n", Header(header))

	fmt.Fprintf(&b, "%-12s %s\n", Dim("Run:"), styleRunID.Render(d.RunID))

	reasonStyle := styleInfo
	if IsFailureDigestReason(d.EndReason) {
		reasonStyle = styleFail
	}
	fmt.Fprintf(&b, "%-12s %s\n", Dim("End reason:"), reasonStyle.Render(d.EndReason))
	if cause := DigestCause(d); cause != "" {
		fmt.Fprintf(&b, "%-12s %s\n", Dim("Cause:"), styleFail.Render(cause))
	}
	fmt.Fprintf(&b, "%-12s %d steps, %s tokens\n", Dim("Budget:"), d.TotalSteps, FormatTokens(d.TotalTokens))

	if len(d.RunErrors) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleFail.Render("Run Errors (last 5):"))
		for _, e := range d.RunErrors {
			fmt.Fprintf(&b, "  %s %s\n", styleFail.Render("•"), compactDigestLine(e))
		}
	}

	if len(d.ToolFailures) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleFail.Render("Tool Failures (last 5):"))
		for _, f := range d.ToolFailures {
			fmt.Fprintf(&b, "  %s %-20s %s\n", styleFail.Render("•"), styleWarn.Render(f.Name), Dim(compactDigestLine(f.Output)))
		}
	}

	if len(d.RetryCluster) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleWarn.Render("Retry Hotspots (>=3 calls):"))
		for _, r := range d.RetryCluster {
			fmt.Fprintf(&b, "  %s %-20s %s\n", styleWarn.Render("•"), r.Name, stylePrimary.Render(fmt.Sprintf("x%d", r.Count)))
		}
	}

	if d.HighTokenStep.StepNum > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleInfo.Render("Resource Peak:"))
		fmt.Fprintf(&b, "  Step %d: %s in, %s out\n",
			d.HighTokenStep.StepNum,
			FormatTokens(d.HighTokenStep.TokensIn),
			FormatTokens(d.HighTokenStep.TokensOut))
	}

	return b.String()
}

func IsFailureDigestReason(reason string) bool {
	return reason == "error" || reason == "budget_exceeded" || strings.HasPrefix(reason, "budget_")
}

// DigestCause returns the highest-signal single-line failure cause for operators.
func DigestCause(d core.RunDigest) string {
	if len(d.RunErrors) > 0 {
		if cause := compactDigestLine(d.RunErrors[len(d.RunErrors)-1]); cause != "" {
			return cause
		}
	}
	if len(d.ToolFailures) > 0 {
		last := d.ToolFailures[len(d.ToolFailures)-1]
		if cause := compactDigestLine(last.Output); cause != "" {
			if last.Name != "" {
				return fmt.Sprintf("%s: %s", last.Name, cause)
			}
			return cause
		}
	}
	if IsFailureDigestReason(d.EndReason) {
		return strings.ReplaceAll(d.EndReason, "_", " ")
	}
	return ""
}

func compactDigestLine(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, raw := range strings.Split(text, "\n") {
		line := collapseWhitespace(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "goroutine ") || strings.HasPrefix(line, "stack traceback:") {
			continue
		}
		line = strings.TrimPrefix(line, "Error: ")
		line = strings.TrimPrefix(line, "error: ")
		if len(line) > 140 {
			line = strings.TrimSpace(line[:139]) + "…"
		}
		return line
	}
	return ""
}
