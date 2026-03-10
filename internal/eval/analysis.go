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

// calculateConfidence determines the confidence score for a given behavior label.
// For now, this is a simple lookup, but can be expanded for dynamic calculation.
func calculateConfidence(labelName string) float64 {
	switch labelName {
	case "tool_hallucination":
		return 1.0
	case "thrashing":
		return 0.8
	case "context_pressure":
		return 1.0
	case "normal":
		// 'normal' is typically an absence of other labels, or a default confidence for a successful run.
		// If a specific label for 'normal' is created, its confidence would be defined here.
		return 1.0
	default:
		// Default confidence for other labels, especially those coming from core.ClassifyRun
		// where confidence is implicitly 1.0 based on current implementation.
		return 1.0
	}
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
	metrics := core.ComputeMetrics(events)
	classification := core.ClassifyRun(events)
	report.Efficiency = metrics.EfficiencyScore

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
		labelName := "tool_hallucination"
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       labelName,
			Confidence: calculateConfidence(labelName),
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
		labelName := "thrashing"
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       labelName,
			Confidence: calculateConfidence(labelName),
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
		labelName := "context_pressure"
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       labelName,
			Confidence: calculateConfidence(labelName),
			Evidence:   fmt.Sprintf("Context compression triggered %d times.", compressionCount),
		})
	}

	if classification.Label != "" && classification.Label != "normal" {
		labelName := classification.Label
		report.Labels = append(report.Labels, BehaviorLabel{
			Name:       labelName,
			Confidence: calculateConfidence(labelName),
			Evidence:   strings.Join(classification.Evidence, "; "),
		})
	}

	return report
}
