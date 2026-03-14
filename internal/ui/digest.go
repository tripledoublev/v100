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
	if d.EndReason == "error" || d.EndReason == "budget_exceeded" {
		reasonStyle = styleFail
	}
	fmt.Fprintf(&b, "%-12s %s\n", Dim("End reason:"), reasonStyle.Render(d.EndReason))
	fmt.Fprintf(&b, "%-12s %d steps, %s tokens\n", Dim("Budget:"), d.TotalSteps, FormatTokens(d.TotalTokens))

	if len(d.RunErrors) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleFail.Render("Run Errors (last 5):"))
		for _, e := range d.RunErrors {
			fmt.Fprintf(&b, "  %s %s\n", styleFail.Render("•"), e)
		}
	}

	if len(d.ToolFailures) > 0 {
		fmt.Fprintf(&b, "\n%s\n", styleFail.Render("Tool Failures (last 5):"))
		for _, f := range d.ToolFailures {
			fmt.Fprintf(&b, "  %s %-20s %s\n", styleFail.Render("•"), styleWarn.Render(f.Name), Dim(f.Output))
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
