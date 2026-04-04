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

// eventsMatch compares two events for meaningful equivalence.
// Returns (match, divergeType, evidence).
func eventsMatch(a, b core.Event) (bool, string, string) {
	if a.Type != b.Type {
		return false, "event_type_mismatch", fmt.Sprintf("A is %s, B is %s", a.Type, b.Type)
	}

	switch a.Type {
	case core.EventToolCall:
		var pA, pB core.ToolCallPayload
		_ = json.Unmarshal(a.Payload, &pA)
		_ = json.Unmarshal(b.Payload, &pB)
		if pA.Name != pB.Name {
			return false, "tool_choice", fmt.Sprintf("A chose %s, B chose %s", pA.Name, pB.Name)
		}
		if pA.Args != pB.Args {
			return false, "tool_args", fmt.Sprintf("Tool %s args differ", pA.Name)
		}

	case core.EventSolverPlan:
		var pA, pB map[string]string
		_ = json.Unmarshal(a.Payload, &pA)
		_ = json.Unmarshal(b.Payload, &pB)
		if pA["plan"] != pB["plan"] {
			return false, "plan_diff", "Agents generated different internal plans."
		}
	}

	return true, "", ""
}

func traceDiffFromSync(sd SyncDiff) TraceDiff {
	diff := TraceDiff{
		RunA:         sd.RunA,
		RunB:         sd.RunB,
		DivergeType:  sd.DivergeType,
		CommonPrefix: len(sd.CommonPrefix()),
		DiffEvidence: sd.DiffEvidence,
	}
	if sd.DivergeIndex >= 0 {
		diff.DivergeStep = sd.DivergeIndex
	}
	if diff.DivergeType == "none" {
		diff.DiffEvidence = ""
	}
	return diff
}

// DiffTraces compares two trajectories to find the first meaningful divergence.
func DiffTraces(runA, runB string, eventsA, eventsB []core.Event) TraceDiff {
	return traceDiffFromSync(SyncTraces(runA, runB, eventsA, eventsB))
}
