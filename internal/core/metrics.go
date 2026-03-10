package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RunMetrics are trace-derived metrics for agent behavior and efficiency.
type RunMetrics struct {
	RunID                        string
	StepsPerRun                  int
	TokensPerRun                 int
	CostPerRunUSD                float64
	TokensPerSuccessfulToolStep  float64
	CostPerSuccessfulToolStepUSD float64
	ToolCallSuccessRate          float64
	ToolRetryRate                float64
	CompressionFrequency         float64
	AvgContextBeforeCompression  float64
	TimePerStepMS                float64
	ToolVsReasoningTimeRatio     float64
	LatencyP50MS                 int64
	LatencyP95MS                 int64
	AutonomousHorizonSteps       int
	EfficiencyScore              float64
	DispatchCalls                int
	DispatchFailures             int
	DispatchSuccessRate          float64
	DispatchTokens               int
	DispatchCostUSD              float64
	DispatchByRole               map[string]int
	DispatchFailuresByRole       map[string]int
}

// RunClassification is an automatic diagnosis from trace behavior.
type RunClassification struct {
	Label    string
	Evidence []string
}

// ComputeMetrics derives quantitative metrics from trace events.
func ComputeMetrics(events []Event) RunMetrics {
	s := ComputeStats(events)
	m := RunMetrics{
		RunID:                  s.RunID,
		StepsPerRun:            s.TotalSteps,
		TokensPerRun:           s.TokensIn + s.TokensOut,
		CostPerRunUSD:          s.TotalCostUSD,
		LatencyP50MS:           Percentile(s.ModelLatencyMS, 50),
		LatencyP95MS:           Percentile(s.ModelLatencyMS, 95),
		DispatchByRole:         map[string]int{},
		DispatchFailuresByRole: map[string]int{},
	}

	var (
		toolResultCount     int
		toolResultSuccess   int
		retryTransitions    int
		prevTool            string
		compressBeforeSum   int
		stepDurationSum     int64
		stepDurationCount   int
		toolDurationSum     int64
		reasonDurationSum   int64
		successfulToolSteps = map[string]bool{}
		horizonReached      bool
		currentStep         int
	)

	for _, ev := range events {
		switch ev.Type {
		case EventStepSummary:
			currentStep++
			var p StepSummaryPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.DurationMS > 0 {
				stepDurationSum += p.DurationMS
				stepDurationCount++
			}
		case EventAgentDispatch:
			var p AgentDispatchPayload
			_ = json.Unmarshal(ev.Payload, &p)
			m.DispatchCalls++
			role := strings.TrimSpace(p.Agent)
			if role == "" {
				role = "anonymous"
			}
			m.DispatchByRole[role]++
		case EventToolCall:
			var p ToolCallPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if prevTool == p.Name && p.Name != "" {
				retryTransitions++
			}
			prevTool = p.Name
		case EventToolResult:
			var p ToolResultPayload
			_ = json.Unmarshal(ev.Payload, &p)
			toolResultCount++
			toolDurationSum += p.DurationMS
			if p.OK {
				toolResultSuccess++
				if ev.StepID != "" {
					successfulToolSteps[ev.StepID] = true
				}
			} else if !horizonReached {
				m.AutonomousHorizonSteps = currentStep
				horizonReached = true
			}
		case EventCompress:
			var p CompressPayload
			_ = json.Unmarshal(ev.Payload, &p)
			compressBeforeSum += p.TokensBefore
		case EventModelResp:
			var p ModelRespPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.DurationMS > 0 {
				reasonDurationSum += p.DurationMS
			}
		case EventAgentEnd:
			var p AgentEndPayload
			_ = json.Unmarshal(ev.Payload, &p)
			m.DispatchTokens += p.UsedTokens
			m.DispatchCostUSD += p.CostUSD
			if !p.OK {
				m.DispatchFailures++
				role := strings.TrimSpace(p.Agent)
				if role == "" {
					role = "anonymous"
				}
				m.DispatchFailuresByRole[role]++
				if !horizonReached {
					m.AutonomousHorizonSteps = currentStep
					horizonReached = true
				}
			}
		case EventRunError:
			if !horizonReached {
				m.AutonomousHorizonSteps = currentStep
				horizonReached = true
			}
		case EventRunEnd:
			var p RunEndPayload
			_ = json.Unmarshal(ev.Payload, &p)
			if p.Reason == "user_exit" && !horizonReached {
				m.AutonomousHorizonSteps = currentStep
				horizonReached = true
			}
		}
	}

	if !horizonReached {
		m.AutonomousHorizonSteps = currentStep
	}

	if toolResultCount > 0 {
		m.ToolCallSuccessRate = float64(toolResultSuccess) / float64(toolResultCount)
	}
	if s.ToolCalls > 0 {
		m.ToolRetryRate = float64(retryTransitions) / float64(s.ToolCalls)
	}
	if s.TotalSteps > 0 {
		m.CompressionFrequency = float64(s.Compressions) / float64(s.TotalSteps)
	}
	if s.Compressions > 0 {
		m.AvgContextBeforeCompression = float64(compressBeforeSum) / float64(s.Compressions)
	}
	if stepDurationCount > 0 {
		m.TimePerStepMS = float64(stepDurationSum) / float64(stepDurationCount)
	} else if s.TotalSteps > 0 {
		m.TimePerStepMS = float64(s.WallClockMS) / float64(s.TotalSteps)
	}
	if reasonDurationSum > 0 {
		m.ToolVsReasoningTimeRatio = float64(toolDurationSum) / float64(reasonDurationSum)
	}
	if len(successfulToolSteps) > 0 {
		m.TokensPerSuccessfulToolStep = float64(m.TokensPerRun) / float64(len(successfulToolSteps))
		m.CostPerSuccessfulToolStepUSD = m.CostPerRunUSD / float64(len(successfulToolSteps))
	}
	if m.DispatchCalls > 0 {
		m.DispatchSuccessRate = float64(m.DispatchCalls-m.DispatchFailures) / float64(m.DispatchCalls)
	}

	m.EfficiencyScore = computeEfficiencyScore(s, m)
	return m
}

func computeEfficiencyScore(s RunStats, m RunMetrics) float64 {
	score := 100.0
	score -= (1.0 - m.ToolCallSuccessRate) * 30.0
	score -= m.ToolRetryRate * 20.0
	score -= m.CompressionFrequency * 10.0
	if strings.HasPrefix(s.EndReason, "budget_") {
		score -= 20.0
	}
	if s.TotalSteps <= 1 && s.ToolCalls == 0 {
		score -= 10.0
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

// ClassifyRun infers a failure mode label from trace patterns.
func ClassifyRun(events []Event) RunClassification {
	s := ComputeStats(events)
	m := ComputeMetrics(events)
	callSeq := toolCallSequence(events)

	if strings.HasPrefix(s.EndReason, "budget_") {
		return RunClassification{
			Label: "budget_exhaustion",
			Evidence: []string{
				fmt.Sprintf("run ended with reason=%s", s.EndReason),
				fmt.Sprintf("steps=%d tool_calls=%d", s.TotalSteps, s.ToolCalls),
			},
		}
	}

	if hasToolLoop(callSeq) {
		return RunClassification{
			Label: "tool_loop",
			Evidence: []string{
				"repeated tool call pattern detected",
				fmt.Sprintf("retry_rate=%.2f", m.ToolRetryRate),
			},
		}
	}

	if m.ToolRetryRate >= 0.35 && m.ToolCallSuccessRate < 0.5 && s.ToolCalls >= 6 {
		return RunClassification{
			Label: "tool_thrashing",
			Evidence: []string{
				fmt.Sprintf("retry_rate=%.2f", m.ToolRetryRate),
				fmt.Sprintf("tool_success_rate=%.2f", m.ToolCallSuccessRate),
			},
		}
	}

	if s.Compressions >= 3 && m.TokensPerSuccessfulToolStep == 0 {
		return RunClassification{
			Label: "context_collapse",
			Evidence: []string{
				fmt.Sprintf("compressions=%d", s.Compressions),
				"no successful tool progress after repeated compression",
			},
		}
	}

	if s.TotalSteps <= 1 && s.ToolCalls == 0 && (s.EndReason == "user_exit" || s.EndReason == "completed") {
		return RunClassification{
			Label: "early_stop",
			Evidence: []string{
				fmt.Sprintf("steps=%d tool_calls=%d", s.TotalSteps, s.ToolCalls),
				fmt.Sprintf("end_reason=%s", s.EndReason),
			},
		}
	}

	if s.EndReason == "error" || s.EndReason == "provider_error" {
		return RunClassification{
			Label: "execution_error",
			Evidence: []string{
				fmt.Sprintf("end_reason=%s", s.EndReason),
				fmt.Sprintf("tool_failures=%d", s.ToolFailures),
			},
		}
	}

	return RunClassification{
		Label: "normal",
		Evidence: []string{
			fmt.Sprintf("end_reason=%s", s.EndReason),
			fmt.Sprintf("tool_success_rate=%.2f", m.ToolCallSuccessRate),
		},
	}
}

func toolCallSequence(events []Event) []string {
	var seq []string
	for _, ev := range events {
		if ev.Type != EventToolCall {
			continue
		}
		var p ToolCallPayload
		_ = json.Unmarshal(ev.Payload, &p)
		if strings.TrimSpace(p.Name) != "" {
			seq = append(seq, p.Name)
		}
	}
	return seq
}

func hasToolLoop(seq []string) bool {
	if len(seq) < 4 {
		return false
	}

	// Same tool streak.
	streak := 1
	for i := 1; i < len(seq); i++ {
		if seq[i] == seq[i-1] {
			streak++
			if streak >= 4 {
				return true
			}
		} else {
			streak = 1
		}
	}

	// Alternating A/B streak.
	if len(seq) >= 6 {
		a, b := seq[0], seq[1]
		if a != b {
			alt := 2
			for i := 2; i < len(seq); i++ {
				expect := a
				if i%2 == 1 {
					expect = b
				}
				if seq[i] == expect {
					alt++
					if alt >= 6 {
						return true
					}
				} else {
					break
				}
			}
		}
	}
	return false
}

// FormatMetrics renders metrics and inferred classification.
func FormatMetrics(m RunMetrics, c RunClassification) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Run:                          %s\n", m.RunID)
	_, _ = fmt.Fprintf(&b, "Classification:               %s\n", c.Label)
	if len(c.Evidence) > 0 {
		b.WriteString("Classification evidence:\n")
		for _, e := range c.Evidence {
			_, _ = fmt.Fprintf(&b, "  - %s\n", e)
		}
	}
	b.WriteString("\nEfficiency metrics:\n")
	_, _ = fmt.Fprintf(&b, "  steps_per_run:              %d\n", m.StepsPerRun)
	_, _ = fmt.Fprintf(&b, "  tokens_per_run:             %d\n", m.TokensPerRun)
	_, _ = fmt.Fprintf(&b, "  cost_per_run_usd:           %.4f\n", m.CostPerRunUSD)
	_, _ = fmt.Fprintf(&b, "  tokens_per_successful_tool_step:  %.2f\n", m.TokensPerSuccessfulToolStep)
	_, _ = fmt.Fprintf(&b, "  cost_per_successful_tool_step_usd: %.4f\n", m.CostPerSuccessfulToolStepUSD)
	_, _ = fmt.Fprintf(&b, "  efficiency_score:           %.1f\n", m.EfficiencyScore)

	b.WriteString("\nBehavior metrics:\n")
	_, _ = fmt.Fprintf(&b, "  tool_call_success_rate:     %.2f\n", m.ToolCallSuccessRate)
	_, _ = fmt.Fprintf(&b, "  tool_retry_rate:            %.2f\n", m.ToolRetryRate)
	_, _ = fmt.Fprintf(&b, "  compression_frequency:      %.2f\n", m.CompressionFrequency)
	_, _ = fmt.Fprintf(&b, "  avg_context_before_compression: %.0f\n", m.AvgContextBeforeCompression)

	b.WriteString("\nTrajectory metrics:\n")
	_, _ = fmt.Fprintf(&b, "  time_per_step_ms:           %.1f\n", m.TimePerStepMS)
	_, _ = fmt.Fprintf(&b, "  latency_p50_ms:             %d\n", m.LatencyP50MS)
	_, _ = fmt.Fprintf(&b, "  latency_p95_ms:             %d\n", m.LatencyP95MS)
	_, _ = fmt.Fprintf(&b, "  autonomous_horizon_steps:   %d\n", m.AutonomousHorizonSteps)
	_, _ = fmt.Fprintf(&b, "  tool_vs_reasoning_time_ratio: %.2f\n", m.ToolVsReasoningTimeRatio)
	if m.DispatchCalls > 0 {
		b.WriteString("\nDispatch metrics:\n")
		_, _ = fmt.Fprintf(&b, "  dispatch_calls:              %d\n", m.DispatchCalls)
		_, _ = fmt.Fprintf(&b, "  dispatch_success_rate:       %.2f\n", m.DispatchSuccessRate)
		_, _ = fmt.Fprintf(&b, "  dispatch_tokens:             %d\n", m.DispatchTokens)
		_, _ = fmt.Fprintf(&b, "  dispatch_cost_usd:           %.4f\n", m.DispatchCostUSD)
		if len(m.DispatchByRole) > 0 {
			b.WriteString("  dispatch_by_role:\n")
			for role, n := range m.DispatchByRole {
				fail := m.DispatchFailuresByRole[role]
				_, _ = fmt.Fprintf(&b, "    - %s: calls=%d failures=%d\n", role, n, fail)
			}
		}
	}

	return b.String()
}

// FormatMetricCompare prints a side-by-side comparison of key metrics.
func FormatMetricCompare(metrics []RunMetrics) string {
	if len(metrics) == 0 {
		return "no runs to compare\n"
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%-24s", "")
	for _, m := range metrics {
		id := m.RunID
		if len(id) > 12 {
			id = id[:12]
		}
		_, _ = fmt.Fprintf(&b, "  %-14s", id)
	}
	b.WriteString("\n")

	row := func(label string, vals []string) {
		_, _ = fmt.Fprintf(&b, "%-24s", label)
		for _, v := range vals {
			_, _ = fmt.Fprintf(&b, "  %-14s", v)
		}
		b.WriteString("\n")
	}
	vals := func(fn func(RunMetrics) string) []string {
		r := make([]string, len(metrics))
		for i, m := range metrics {
			r[i] = fn(m)
		}
		return r
	}

	row("EfficiencyScore", vals(func(m RunMetrics) string { return fmt.Sprintf("%.1f", m.EfficiencyScore) }))
	row("ToolSuccessRate", vals(func(m RunMetrics) string { return fmt.Sprintf("%.2f", m.ToolCallSuccessRate) }))
	row("ToolRetryRate", vals(func(m RunMetrics) string { return fmt.Sprintf("%.2f", m.ToolRetryRate) }))
	row("HorizonSteps", vals(func(m RunMetrics) string { return fmt.Sprintf("%d", m.AutonomousHorizonSteps) }))
	row("CompFreq", vals(func(m RunMetrics) string { return fmt.Sprintf("%.2f", m.CompressionFrequency) }))
	row("TimePerStep", vals(func(m RunMetrics) string { return fmt.Sprintf("%.1fms", m.TimePerStepMS) }))
	row("Tok/SuccessStep", vals(func(m RunMetrics) string { return fmt.Sprintf("%.0f", m.TokensPerSuccessfulToolStep) }))
	row("Cost/Run", vals(func(m RunMetrics) string { return fmt.Sprintf("$%.4f", m.CostPerRunUSD) }))
	row("DispatchSR", vals(func(m RunMetrics) string { return fmt.Sprintf("%.2f", m.DispatchSuccessRate) }))

	return b.String()
}
