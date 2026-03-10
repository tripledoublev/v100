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

func trimFloatSuffix(v string) string {
	v = strings.Replace(v, ".0M", "M", 1)
	v = strings.Replace(v, ".0k", "k", 1)
	return v
}
