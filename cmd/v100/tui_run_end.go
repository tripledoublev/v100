package main

import (
	"context"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func shouldSummarizeTUIRunEnd(reason string) bool {
	return reason != "error"
}

func emitFinalTUIRunEnd(loop *core.Loop, prov providers.Provider, model, reason string) error {
	summary := ""
	if shouldSummarizeTUIRunEnd(reason) {
		summary = generateRunSummary(context.Background(), prov, model, loop.Messages)
	}
	return loop.EmitRunEnd(reason, summary)
}
