package eval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
)

// BehaviorLabel identifies a specific pattern in an agent's trajectory.
type BehaviorLabel struct {
	Name       string  `json:"name"`       // e.g., "thrashing", "hallucination", "efficient"
	Confidence float64 `json:"confidence"` // 0.0 to 1.0
	Evidence   string  `json:"evidence"`   // Why this label was applied
}

// AnalysisReport holds the automated findings for a single run.
type AnalysisReport struct {
	RunID      string          `json:"run_id"`
	Labels     []BehaviorLabel `json:"labels"`
	ToolErrors int             `json:"tool_errors"`
	Efficiency float64         `json:"efficiency_score"`
}

// AnalyzeTrajectory runs heuristic classifiers over a trace to detect behavioral patterns.
func AnalyzeTrajectory(events []core.Event) AnalysisReport {
	report := AnalysisReport{
		Labels: []BehaviorLabel{},
	}

	if len(events) == 0 {
		return report
	}
	report.RunID = events[0].RunID

	// 1. Detect Tool Hallucinations (Model tried a tool that doesn't exist)
	hallucinations := []string{}
	for _, ev := range events {
		if ev.Type == core.EventToolResult {
			var p core.ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if !p.OK && strings.Contains(p.Output, "not found or not enabled") {
				hallucinations = append(hallucinations, p.Name)
				report.ToolErrors++
			}
		}
	}
	if len(hallucinations) > 0 {
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       "tool_hallucination",
			Confidence: 1.0,
			Evidence:   fmt.Sprintf("Agent attempted non-existent tools: %s", strings.Join(hallucinations, ", ")),
		})
	}

	// 2. Detect Thrashing (Repeatedly calling the same tool with same args)
	toolCalls := make(map[string]int)
	for _, ev := range events {
		if ev.Type == core.EventToolCall {
			var p core.ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			key := p.Name + p.Args
			toolCalls[key]++
		}
	}
	maxRepeats := 0
	for _, count := range toolCalls {
		if count > maxRepeats {
			maxRepeats = count
		}
	}
	if maxRepeats >= 3 {
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       "thrashing",
			Confidence: 0.8,
			Evidence:   fmt.Sprintf("Tool call repeated %d times with identical arguments.", maxRepeats),
		})
	}

	// 3. Detect Context Pressure (Compression triggered)
	compressionCount := 0
	for _, ev := range events {
		if ev.Type == core.EventCompress {
			compressionCount++
		}
	}
	if compressionCount > 0 {
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       "context_pressure",
			Confidence: 1.0,
			Evidence:   fmt.Sprintf("Context compression triggered %d times.", compressionCount),
		})
	}

	// 4. Calculate Research-Grade Efficiency
	// Formula: (Successful Steps) / (Total Model Calls)
	modelCalls := 0
	for _, ev := range events {
		if ev.Type == core.EventModelCall {
			modelCalls++
		}
	}
	if modelCalls > 0 {
		report.Efficiency = float64(len(events)-report.ToolErrors) / float64(modelCalls) * 10.0 // Scaled to 0-100ish
	}

	return report
}
