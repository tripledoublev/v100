package eval

import (
	"encoding/json"
	"fmt"

	"github.com/tripledoublev/v100/internal/core"
)

// TraceDiff identifies the point and nature of divergence between two runs.
type TraceDiff struct {
	RunA         string
	RunB         string
	DivergeStep  int
	DivergeType  string // "tool_choice", "tool_args", "plan_diff", "none"
	CommonPrefix int    // number of identical events
	DiffEvidence string
}

// DiffTraces compares two trajectories to find the first meaningful divergence.
func DiffTraces(runA, runB string, eventsA, eventsB []core.Event) TraceDiff {
	diff := TraceDiff{
		RunA:        runA,
		RunB:        runB,
		DivergeType: "none",
	}

	maxLen := len(eventsA)
	if len(eventsB) < maxLen {
		maxLen = len(eventsB)
	}

	for i := 0; i < maxLen; i++ {
		evA := eventsA[i]
		evB := eventsB[i]

		if evA.Type != evB.Type {
			diff.DivergeStep = i
			diff.DivergeType = "event_type_mismatch"
			diff.DiffEvidence = fmt.Sprintf("Event %d: A is %s, B is %s", i, evA.Type, evB.Type)
			return diff
		}

		// Check for specific payload divergences
		switch evA.Type {
		case core.EventToolCall:
			var pA, pB core.ToolCallPayload
			_ = json.Unmarshal(evA.Payload, &pA)
			_ = json.Unmarshal(evB.Payload, &pB)
			if pA.Name != pB.Name {
				diff.DivergeStep = i
				diff.DivergeType = "tool_choice"
				diff.DiffEvidence = fmt.Sprintf("A chose %s, B chose %s", pA.Name, pB.Name)
				return diff
			}
			if pA.Args != pB.Args {
				diff.DivergeStep = i
				diff.DivergeType = "tool_args"
				diff.DiffEvidence = fmt.Sprintf("Tool %s args differ", pA.Name)
				return diff
			}

		case core.EventSolverPlan:
			var pA, pB map[string]string
			_ = json.Unmarshal(evA.Payload, &pA)
			_ = json.Unmarshal(evB.Payload, &pB)
			if pA["plan"] != pB["plan"] {
				diff.DivergeStep = i
				diff.DivergeType = "plan_diff"
				diff.DiffEvidence = "Agents generated different internal plans."
				return diff
			}
		}

		diff.CommonPrefix++
	}

	if len(eventsA) != len(eventsB) {
		diff.DivergeStep = maxLen
		diff.DivergeType = "length_mismatch"
		diff.DiffEvidence = fmt.Sprintf("Trace A has %d events, B has %d", len(eventsA), len(eventsB))
	}

	return diff
}
