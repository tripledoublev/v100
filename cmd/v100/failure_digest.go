package main

import (
	"fmt"
	"io"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func maybePrintFailureDigest(out io.Writer, tracePath, reason string) {
	if out == nil || !ui.IsFailureDigestReason(reason) {
		return
	}
	events, err := core.ReadAll(tracePath)
	if err != nil {
		return
	}
	digest := core.ComputeDigest(events)
	if digest.EndReason == "" {
		digest.EndReason = reason
	}
	if digest.RunID == "" {
		for _, ev := range events {
			if ev.RunID != "" {
				digest.RunID = ev.RunID
				break
			}
		}
	}
	if ui.DigestCause(digest) == "" && len(digest.RunErrors) == 0 && len(digest.ToolFailures) == 0 {
		return
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprint(out, ui.FormatDigestStyled(digest))
}
