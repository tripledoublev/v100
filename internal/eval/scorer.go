package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

// ScoreResult is the outcome of a scorer evaluation.
type ScoreResult struct {
	Score string  `json:"score"` // "pass", "fail", "partial"
	Value float64 `json:"value"` // 0.0-1.0 for numeric scoring
	Notes string  `json:"notes,omitempty"`
}

// Scorer evaluates a run trace against expectations.
type Scorer interface {
	Name() string
	Score(ctx context.Context, trace []core.Event, expected string) (ScoreResult, error)
}

// LookupScorer returns a scorer by name. Names:
// "exact_match", "contains", "regex", "script:<command>", "model_graded"
func LookupScorer(name string, prov providers.Provider, model string) (Scorer, error) {
	switch {
	case name == "exact_match":
		return ExactMatch{}, nil
	case name == "contains":
		return Contains{}, nil
	case name == "regex":
		return RegexMatch{}, nil
	case strings.HasPrefix(name, "script:"):
		return Script{Command: strings.TrimPrefix(name, "script:")}, nil
	case name == "model_graded":
		if prov == nil {
			return nil, fmt.Errorf("model_graded scorer requires a provider")
		}
		return &ModelGraded{Provider: prov, Model: model}, nil
	default:
		return nil, fmt.Errorf("unknown scorer: %q", name)
	}
}

// lastAssistantText extracts the final assistant text from a trace.
func lastAssistantText(events []core.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != core.EventModelResp {
			continue
		}
		var p core.ModelRespPayload
		if json.Unmarshal(events[i].Payload, &p) == nil {
			if t := strings.TrimSpace(p.Text); t != "" {
				return t
			}
		}
	}
	return ""
}

// ExactMatch scores pass if the last assistant text matches expected exactly (trimmed).
type ExactMatch struct{}

func (ExactMatch) Name() string { return "exact_match" }
func (ExactMatch) Score(_ context.Context, trace []core.Event, expected string) (ScoreResult, error) {
	last := lastAssistantText(trace)
	if strings.TrimSpace(last) == strings.TrimSpace(expected) {
		return ScoreResult{Score: "pass", Value: 1.0}, nil
	}
	return ScoreResult{Score: "fail", Value: 0.0, Notes: "text mismatch"}, nil
}

// Contains scores pass if the last assistant text contains the expected string.
type Contains struct{}

func (Contains) Name() string { return "contains" }
func (Contains) Score(_ context.Context, trace []core.Event, expected string) (ScoreResult, error) {
	last := lastAssistantText(trace)
	if strings.Contains(last, expected) {
		return ScoreResult{Score: "pass", Value: 1.0}, nil
	}
	return ScoreResult{Score: "fail", Value: 0.0, Notes: "expected substring not found"}, nil
}

// RegexMatch scores pass if the last assistant text matches the expected regex.
type RegexMatch struct{}

func (RegexMatch) Name() string { return "regex" }
func (RegexMatch) Score(_ context.Context, trace []core.Event, expected string) (ScoreResult, error) {
	re, err := regexp.Compile(expected)
	if err != nil {
		return ScoreResult{}, fmt.Errorf("regex scorer: invalid pattern: %w", err)
	}
	last := lastAssistantText(trace)
	if re.MatchString(last) {
		return ScoreResult{Score: "pass", Value: 1.0}, nil
	}
	return ScoreResult{Score: "fail", Value: 0.0, Notes: "regex not matched"}, nil
}

// Script runs an external command with the response on stdin.
// Exit 0 = pass, 1 = fail, 2 = partial. Stdout is captured as notes.
type Script struct {
	Command string
}

func (s Script) Name() string { return "script" }
func (s Script) Score(ctx context.Context, trace []core.Event, expected string) (ScoreResult, error) {
	last := lastAssistantText(trace)
	cmd := exec.CommandContext(ctx, "sh", "-c", s.Command)
	cmd.Stdin = strings.NewReader(last)
	cmd.Env = append(cmd.Environ(), "EXPECTED="+expected)
	out, err := cmd.CombinedOutput()
	notes := strings.TrimSpace(string(out))

	if err == nil {
		return ScoreResult{Score: "pass", Value: 1.0, Notes: notes}, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		switch exitErr.ExitCode() {
		case 1:
			return ScoreResult{Score: "fail", Value: 0.0, Notes: notes}, nil
		case 2:
			return ScoreResult{Score: "partial", Value: 0.5, Notes: notes}, nil
		}
	}
	return ScoreResult{}, fmt.Errorf("script scorer: %w: %s", err, notes)
}

// ModelGraded uses an LLM as a judge to evaluate the response.
type ModelGraded struct {
	Provider providers.Provider
	Model    string
	Prompt   string // custom template; uses default if empty
}

const defaultGraderPrompt = `You are an automated grader. Evaluate whether the response correctly answers the task.

Task expected answer: {{expected}}

Actual response: {{response}}

Reply with exactly one of: PASS, FAIL, or PARTIAL
Then a brief explanation on the next line.`

func (g *ModelGraded) Name() string { return "model_graded" }
func (g *ModelGraded) Score(ctx context.Context, trace []core.Event, expected string) (ScoreResult, error) {
	response := lastAssistantText(trace)
	tmpl := g.Prompt
	if tmpl == "" {
		tmpl = defaultGraderPrompt
	}
	prompt := strings.ReplaceAll(tmpl, "{{expected}}", expected)
	prompt = strings.ReplaceAll(prompt, "{{response}}", response)

	resp, err := g.Provider.Complete(ctx, providers.CompleteRequest{
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		Model: g.Model,
		GenParams: providers.GenParams{
			Temperature: ptrFloat64(0.0),
			MaxTokens:   256,
		},
	})
	if err != nil {
		return ScoreResult{}, fmt.Errorf("model_graded: %w", err)
	}

	text := strings.TrimSpace(resp.AssistantText)
	lines := strings.SplitN(text, "\n", 2)
	verdict := strings.ToUpper(strings.TrimSpace(lines[0]))
	notes := ""
	if len(lines) > 1 {
		notes = strings.TrimSpace(lines[1])
	}

	switch {
	case strings.Contains(verdict, "PASS"):
		return ScoreResult{Score: "pass", Value: 1.0, Notes: notes}, nil
	case strings.Contains(verdict, "PARTIAL"):
		return ScoreResult{Score: "partial", Value: 0.5, Notes: notes}, nil
	default:
		return ScoreResult{Score: "fail", Value: 0.0, Notes: notes}, nil
	}
}

func ptrFloat64(v float64) *float64 { return &v }
