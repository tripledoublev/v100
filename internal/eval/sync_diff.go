package eval

import (
	"fmt"

	"github.com/tripledoublev/v100/internal/core"
)

// SegmentStatus identifies how a pair of events relates in a synchronized diff.
type SegmentStatus int

const (
	// SegmentMatch means both events are meaningfully equivalent.
	SegmentMatch SegmentStatus = iota
	// SegmentDiverge marks the first point of meaningful difference.
	SegmentDiverge
	// SegmentTailA means this event exists only in trace A at this aligned row.
	SegmentTailA
	// SegmentTailB means this event exists only in trace B at this aligned row.
	SegmentTailB
)

// SyncSegment is one aligned row in a synchronized trace comparison.
type SyncSegment struct {
	EventA *core.Event // nil if only in B
	EventB *core.Event // nil if only in A
	Status SegmentStatus
}

// SyncDiff is the full synchronized comparison of two traces.
// It provides everything a side-by-side viewer needs: matched pairs,
// the divergence point, and the remaining tails from each trace.
type SyncDiff struct {
	RunA         string
	RunB         string
	Segments     []SyncSegment
	DivergeIndex int    // index into Segments of first divergence; -1 if identical
	DivergeType  string // "tool_choice", "tool_args", "plan_diff", "event_type_mismatch", "length_mismatch", "none"
	DiffEvidence string
}

// CommonPrefix returns the segments before the divergence point.
func (d SyncDiff) CommonPrefix() []SyncSegment {
	if d.DivergeIndex < 0 {
		return d.Segments
	}
	return d.Segments[:d.DivergeIndex]
}

type syncAlignStep uint8

const (
	syncAlignMatch syncAlignStep = iota
	syncAlignDiverge
	syncAlignTailA
	syncAlignTailB
)

// SyncTraces builds a SyncDiff from two event slices using a minimal-cost
// alignment. This allows traces to realign after insertions/deletions instead
// of forcing every later row into divergence.
func SyncTraces(runA, runB string, eventsA, eventsB []core.Event) SyncDiff {
	sd := SyncDiff{
		RunA:         runA,
		RunB:         runB,
		DivergeIndex: -1,
		DivergeType:  "none",
	}

	n := len(eventsA)
	m := len(eventsB)
	costs := make([][]int, n+1)
	steps := make([][]syncAlignStep, n+1)
	for i := range costs {
		costs[i] = make([]int, m+1)
		steps[i] = make([]syncAlignStep, m+1)
	}

	for i := n - 1; i >= 0; i-- {
		costs[i][m] = costs[i+1][m] + 1
		steps[i][m] = syncAlignTailA
	}
	for j := m - 1; j >= 0; j-- {
		costs[n][j] = costs[n][j+1] + 1
		steps[n][j] = syncAlignTailB
	}

	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			match, _, _ := eventsMatch(eventsA[i], eventsB[j])
			bestCost := costs[i+1][j+1]
			bestStep := syncAlignMatch
			if !match {
				bestCost = costs[i+1][j+1] + 1
				bestStep = syncAlignDiverge
			}

			if cost := costs[i+1][j] + 1; cost < bestCost {
				bestCost = cost
				bestStep = syncAlignTailA
			}
			if cost := costs[i][j+1] + 1; cost < bestCost {
				bestCost = cost
				bestStep = syncAlignTailB
			}

			costs[i][j] = bestCost
			steps[i][j] = bestStep
		}
	}

	i, j := 0, 0
	for i < n || j < m {
		step := steps[i][j]
		switch step {
		case syncAlignMatch:
			sd.Segments = append(sd.Segments, SyncSegment{
				EventA: &eventsA[i],
				EventB: &eventsB[j],
				Status: SegmentMatch,
			})
			i++
			j++
		case syncAlignDiverge:
			match, dtype, evidence := eventsMatch(eventsA[i], eventsB[j])
			if match {
				panic("syncAlignDiverge chosen for matching events")
			}
			if sd.DivergeIndex < 0 {
				sd.DivergeIndex = len(sd.Segments)
				sd.DivergeType = dtype
				sd.DiffEvidence = evidence
			}
			sd.Segments = append(sd.Segments, SyncSegment{
				EventA: &eventsA[i],
				EventB: &eventsB[j],
				Status: SegmentDiverge,
			})
			i++
			j++
		case syncAlignTailA:
			if sd.DivergeIndex < 0 {
				sd.DivergeIndex = len(sd.Segments)
				sd.DivergeType = "length_mismatch"
				sd.DiffEvidence = fmt.Sprintf("trace A has unmatched %s at event %d", eventsA[i].Type, i)
			}
			sd.Segments = append(sd.Segments, SyncSegment{
				EventA: &eventsA[i],
				Status: SegmentTailA,
			})
			i++
		case syncAlignTailB:
			if sd.DivergeIndex < 0 {
				sd.DivergeIndex = len(sd.Segments)
				sd.DivergeType = "length_mismatch"
				sd.DiffEvidence = fmt.Sprintf("trace B has unmatched %s at event %d", eventsB[j].Type, j)
			}
			sd.Segments = append(sd.Segments, SyncSegment{
				EventB: &eventsB[j],
				Status: SegmentTailB,
			})
			j++
		default:
			panic("unknown sync alignment step")
		}
	}
	return sd
}
