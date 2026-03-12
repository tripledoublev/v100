package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tripledoublev/v100/internal/providers"
)

// RunStats holds computed statistics from a trace.
type RunStats struct {
	RunID          string
	Provider       string
	Model          string
	ModelMetadata  providers.ModelMetadata
	TotalSteps     int
	TokensIn       int
	TokensOut      int
	TotalCostUSD   float64
	WallClockMS    int64
	ModelCalls     int
	ModelLatencyMS []int64
	ToolCalls      int
	ToolUsage      map[string]int
	ToolFailures   int
	Compressions   int
	WatchdogFires  int
	EndReason      string
	Score          string
}

// ComputeStats derives RunStats from a slice of trace events.
func ComputeStats(events []Event) RunStats {
	s := RunStats{ToolUsage: make(map[string]int)}

	var firstTS, lastTS int64

	for _, ev := range events {
		ts := ev.TS.UnixMilli()
		if firstTS == 0 || ts < firstTS {
			firstTS = ts
		}
		if ts > lastTS {
			lastTS = ts
		}

		switch ev.Type {
		case EventRunStart:
			var p RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			s.RunID = ev.RunID
			s.Provider = p.Provider
			s.Model = p.Model
			s.ModelMetadata = p.ModelMetadata

		case EventModelResp:
			var p ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			s.ModelCalls++
			s.TokensIn += p.Usage.InputTokens
			s.TokensOut += p.Usage.OutputTokens
			s.TotalCostUSD += p.Usage.CostUSD
			if p.DurationMS > 0 {
				s.ModelLatencyMS = append(s.ModelLatencyMS, p.DurationMS)
			}

		case EventToolCall:
			var p ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			s.ToolCalls++
			s.ToolUsage[p.Name]++

		case EventToolResult:
			var p ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if !p.OK {
				s.ToolFailures++
			}

		case EventStepSummary:
			s.TotalSteps++

		case EventCompress:
			s.Compressions++

		case EventHookIntervention:
			var p HookInterventionPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.Reason == "inspection_watchdog" || p.Reason == "read_heavy_watchdog" {
				s.WatchdogFires++
			}

		case EventRunEnd:
			var p RunEndPayload
			_ = json.Unmarshal(ev.Payload, &p)
			s.EndReason = p.Reason
		}
	}

	s.WallClockMS = lastTS - firstTS

	// If no step.summary events were emitted (aborted/errored runs),
	// infer step count from the number of model responses.
	if s.TotalSteps == 0 && s.ModelCalls > 0 {
		s.TotalSteps = 1 // at least one partial step occurred
	}

	sort.Slice(s.ModelLatencyMS, func(i, j int) bool { return s.ModelLatencyMS[i] < s.ModelLatencyMS[j] })
	return s
}

// Percentile returns the p-th percentile from a sorted int64 slice.
func Percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// FormatStats returns a human-readable stats table.
func FormatStats(s RunStats) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Run:          %s\n", s.RunID)
	_, _ = fmt.Fprintf(&b, "Provider:     %s\n", s.Provider)
	_, _ = fmt.Fprintf(&b, "Model:        %s\n", s.Model)
	if s.ModelMetadata.ContextSize > 0 {
		_, _ = fmt.Fprintf(&b, "Context:      %s\n", FormatContextSize(s.ModelMetadata.ContextSize))
	}
	if pricing := FormatModelPricing(s.ModelMetadata); pricing != "-" {
		_, _ = fmt.Fprintf(&b, "Pricing:      %s\n", pricing)
	}
	_, _ = fmt.Fprintf(&b, "Steps:        %d\n", s.TotalSteps)
	_, _ = fmt.Fprintf(&b, "Model calls:  %d\n", s.ModelCalls)
	_, _ = fmt.Fprintf(&b, "Tokens in:    %d\n", s.TokensIn)
	_, _ = fmt.Fprintf(&b, "Tokens out:   %d\n", s.TokensOut)
	_, _ = fmt.Fprintf(&b, "Cost:         $%.4f\n", s.TotalCostUSD)
	_, _ = fmt.Fprintf(&b, "Wall clock:   %dms\n", s.WallClockMS)
	if len(s.ModelLatencyMS) > 0 {
		_, _ = fmt.Fprintf(&b, "Latency p50:  %dms\n", Percentile(s.ModelLatencyMS, 50))
		_, _ = fmt.Fprintf(&b, "Latency p95:  %dms\n", Percentile(s.ModelLatencyMS, 95))
		_, _ = fmt.Fprintf(&b, "Latency max:  %dms\n", s.ModelLatencyMS[len(s.ModelLatencyMS)-1])
	}
	_, _ = fmt.Fprintf(&b, "Tool calls:   %d\n", s.ToolCalls)
	_, _ = fmt.Fprintf(&b, "Tool fails:   %d\n", s.ToolFailures)
	_, _ = fmt.Fprintf(&b, "Compressions: %d\n", s.Compressions)
	_, _ = fmt.Fprintf(&b, "End reason:   %s\n", s.EndReason)
	if s.Score != "" {
		_, _ = fmt.Fprintf(&b, "Score:        %s\n", s.Score)
	}

	if len(s.ToolUsage) > 0 {
		b.WriteString("\nTool usage:\n")
		type kv struct {
			k string
			v int
		}
		var pairs []kv
		for k, v := range s.ToolUsage {
			pairs = append(pairs, kv{k, v})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
		for _, p := range pairs {
			_, _ = fmt.Fprintf(&b, "  %-20s %d\n", p.k, p.v)
		}
	}
	return b.String()
}

// FormatCompare prints a side-by-side comparison of multiple RunStats.
func FormatCompare(stats []RunStats) string {
	if len(stats) == 0 {
		return "no runs to compare\n"
	}
	var b strings.Builder

	// Header
	_, _ = fmt.Fprintf(&b, "%-14s", "")
	for _, s := range stats {
		id := s.RunID
		if len(id) > 12 {
			id = id[:12]
		}
		_, _ = fmt.Fprintf(&b, "  %-14s", id)
	}
	b.WriteString("\n")

	row := func(label string, vals []string) {
		_, _ = fmt.Fprintf(&b, "%-14s", label)
		for _, v := range vals {
			_, _ = fmt.Fprintf(&b, "  %-14s", v)
		}
		b.WriteString("\n")
	}

	vals := func(fn func(RunStats) string) []string {
		r := make([]string, len(stats))
		for i, s := range stats {
			r[i] = fn(s)
		}
		return r
	}

	row("Provider", vals(func(s RunStats) string { return s.Provider }))
	row("Model", vals(func(s RunStats) string { return s.Model }))
	row("Context", vals(func(s RunStats) string { return FormatContextSize(s.ModelMetadata.ContextSize) }))
	row("Pricing", vals(func(s RunStats) string { return FormatModelPricing(s.ModelMetadata) }))
	row("Steps", vals(func(s RunStats) string { return fmt.Sprintf("%d", s.TotalSteps) }))
	row("Tokens", vals(func(s RunStats) string { return fmt.Sprintf("%d/%d", s.TokensIn, s.TokensOut) }))
	row("Cost", vals(func(s RunStats) string { return fmt.Sprintf("$%.4f", s.TotalCostUSD) }))
	row("Duration", vals(func(s RunStats) string { return fmt.Sprintf("%dms", s.WallClockMS) }))
	row("Model p50", vals(func(s RunStats) string { return fmt.Sprintf("%dms", Percentile(s.ModelLatencyMS, 50)) }))
	row("Tool calls", vals(func(s RunStats) string { return fmt.Sprintf("%d", s.ToolCalls) }))
	row("Tool fails", vals(func(s RunStats) string { return fmt.Sprintf("%d", s.ToolFailures) }))
	row("Score", vals(func(s RunStats) string { return s.Score }))
	row("End", vals(func(s RunStats) string { return s.EndReason }))

	return b.String()
}

func FormatContextSize(size int) string {
	if size <= 0 {
		return "-"
	}
	switch {
	case size >= 1_000_000:
		return trimFloatSuffix(fmt.Sprintf("%.1fM", float64(size)/1_000_000))
	case size >= 1_000:
		return trimFloatSuffix(fmt.Sprintf("%.1fk", float64(size)/1_000))
	default:
		return fmt.Sprintf("%d", size)
	}
}

func FormatModelPricing(m providers.ModelMetadata) string {
	if m.IsFree {
		return "free"
	}
	if m.CostPer1MIn <= 0 && m.CostPer1MOut <= 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f/$%.2f", m.CostPer1MIn, m.CostPer1MOut)
}

// ── Failure Digest ────────────────────────────────────────────────────────────

// RunDigest is a compact failure-focused summary of a completed run.
type RunDigest struct {
	RunID         string
	EndReason     string
	ToolFailures  []DigestToolFailure // last up to 5 failed tool calls
	RunErrors     []string            // run.error messages
	RetryCluster  []DigestRetryEntry  // tools called 3+ times (retry hotspot)
	HighTokenStep DigestStep          // step with most input tokens
	TotalSteps    int
	TotalTokens   int
}

// DigestToolFailure captures a single failed tool call.
type DigestToolFailure struct {
	Name   string
	Output string // truncated error message
}

// DigestRetryEntry is a tool that was called repeatedly within the run.
type DigestRetryEntry struct {
	Name  string
	Count int
}

// DigestStep identifies a step by number and token cost.
type DigestStep struct {
	StepNum    int
	TokensIn   int
	TokensOut  int
}

// ComputeDigest builds a RunDigest from trace events.
func ComputeDigest(events []Event) RunDigest {
	d := RunDigest{}
	toolCounts := map[string]int{}

	for _, ev := range events {
		switch ev.Type {
		case EventRunStart:
			var p RunStartPayload
			_ = json.Unmarshal(ev.Payload, &p)
			d.RunID = ev.RunID

		case EventRunEnd:
			var p RunEndPayload
			_ = json.Unmarshal(ev.Payload, &p)
			d.EndReason = p.Reason

		case EventRunError:
			var p RunErrorPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.Error != "" {
				d.RunErrors = append(d.RunErrors, p.Error)
			}

		case EventStepSummary:
			var p StepSummaryPayload
			_ = json.Unmarshal(ev.Payload, &p)
			d.TotalSteps++
			d.TotalTokens += p.InputTokens + p.OutputTokens
			if p.InputTokens > d.HighTokenStep.TokensIn {
				d.HighTokenStep = DigestStep{
					StepNum:   d.TotalSteps,
					TokensIn:  p.InputTokens,
					TokensOut: p.OutputTokens,
				}
			}

		case EventToolCall:
			var p ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			toolCounts[p.Name]++

		case EventToolResult:
			var p ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if !p.OK {
				out := p.Output
				if len(out) > 120 {
					out = out[:120] + "…"
				}
				d.ToolFailures = append(d.ToolFailures, DigestToolFailure{Name: p.Name, Output: out})
			}
		}
	}

	// Retry clusters: tools called 3+ times
	for name, count := range toolCounts {
		if count >= 3 {
			d.RetryCluster = append(d.RetryCluster, DigestRetryEntry{Name: name, Count: count})
		}
	}
	sort.Slice(d.RetryCluster, func(i, j int) bool {
		return d.RetryCluster[i].Count > d.RetryCluster[j].Count
	})

	// Keep only the last 5 tool failures
	if len(d.ToolFailures) > 5 {
		d.ToolFailures = d.ToolFailures[len(d.ToolFailures)-5:]
	}

	// Cap run errors at 5
	if len(d.RunErrors) > 5 {
		d.RunErrors = d.RunErrors[len(d.RunErrors)-5:]
	}

	return d
}

// FormatDigest returns a compact human-readable failure digest.
func FormatDigest(d RunDigest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Run:        %s\n", d.RunID)
	fmt.Fprintf(&b, "End reason: %s\n", d.EndReason)
	fmt.Fprintf(&b, "Steps:      %d   Tokens: %d\n", d.TotalSteps, d.TotalTokens)

	if len(d.RunErrors) > 0 {
		b.WriteString("\nRun errors (last 5):\n")
		for _, e := range d.RunErrors {
			fmt.Fprintf(&b, "  • %s\n", e)
		}
	}

	if len(d.ToolFailures) > 0 {
		b.WriteString("\nTool failures (last 5):\n")
		for _, f := range d.ToolFailures {
			fmt.Fprintf(&b, "  • %-20s %s\n", f.Name, f.Output)
		}
	}

	if len(d.RetryCluster) > 0 {
		b.WriteString("\nRetry hotspots (≥3 calls):\n")
		for _, r := range d.RetryCluster {
			fmt.Fprintf(&b, "  • %-20s ×%d\n", r.Name, r.Count)
		}
	}

	if d.HighTokenStep.StepNum > 0 {
		fmt.Fprintf(&b, "\nHigh-token step: step %d  in=%d out=%d\n",
			d.HighTokenStep.StepNum, d.HighTokenStep.TokensIn, d.HighTokenStep.TokensOut)
	}

	return b.String()
}

func trimFloatSuffix(v string) string {
	v = strings.Replace(v, ".0M", "M", 1)
	v = strings.Replace(v, ".0k", "k", 1)
	return v
}
