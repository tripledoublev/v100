package eval_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/eval"
)

func makeTrace(text string) []core.Event {
	payload, _ := json.Marshal(core.ModelRespPayload{Text: text})
	return []core.Event{
		{Type: core.EventModelResp, Payload: payload},
	}
}

func TestExactMatch(t *testing.T) {
	ctx := context.Background()
	s := eval.ExactMatch{}

	r, err := s.Score(ctx, makeTrace("hello world"), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass, got %s", r.Score)
	}

	r, err = s.Score(ctx, makeTrace("hello world"), "goodbye")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "fail" {
		t.Errorf("expected fail, got %s", r.Score)
	}
}

func TestExactMatchTrimmed(t *testing.T) {
	ctx := context.Background()
	s := eval.ExactMatch{}

	r, err := s.Score(ctx, makeTrace("  hello  "), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass for trimmed match, got %s", r.Score)
	}
}

func TestContains(t *testing.T) {
	ctx := context.Background()
	s := eval.Contains{}

	r, err := s.Score(ctx, makeTrace("the answer is 42 ok"), "42")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass, got %s", r.Score)
	}

	r, err = s.Score(ctx, makeTrace("no match"), "42")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "fail" {
		t.Errorf("expected fail, got %s", r.Score)
	}

	// Case-insensitive: model answers "Yes." but bench expects "yes"
	r, err = s.Score(ctx, makeTrace("Yes."), "yes")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass for 'Yes.' vs 'yes', got %s", r.Score)
	}

	// Case-insensitive: upper-case answer
	r, err = s.Score(ctx, makeTrace("The answer is YES"), "yes")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass for 'YES' vs 'yes', got %s", r.Score)
	}
}

func TestRegexMatch(t *testing.T) {
	ctx := context.Background()
	s := eval.RegexMatch{}

	r, err := s.Score(ctx, makeTrace("result: 42"), `\d+`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass, got %s", r.Score)
	}

	r, err = s.Score(ctx, makeTrace("no numbers"), `\d+`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "fail" {
		t.Errorf("expected fail, got %s", r.Score)
	}
}

func TestRegexMatchInvalid(t *testing.T) {
	ctx := context.Background()
	s := eval.RegexMatch{}

	_, err := s.Score(ctx, makeTrace("test"), `[invalid`)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestScript(t *testing.T) {
	ctx := context.Background()
	s := eval.Script{Command: `grep -q "hello" && exit 0 || exit 1`}

	r, err := s.Score(ctx, makeTrace("hello world"), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "pass" {
		t.Errorf("expected pass, got %s", r.Score)
	}

	r, err = s.Score(ctx, makeTrace("goodbye"), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "fail" {
		t.Errorf("expected fail, got %s", r.Score)
	}
}

func TestLookupScorer(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"exact_match", false},
		{"contains", false},
		{"regex", false},
		{"script:echo ok", false},
		{"model_graded", true}, // needs provider
		{"unknown", true},
	}

	for _, tt := range tests {
		s, err := eval.LookupScorer(tt.name, nil, "")
		if tt.wantErr {
			if err == nil {
				t.Errorf("LookupScorer(%q): expected error", tt.name)
			}
		} else {
			if err != nil {
				t.Errorf("LookupScorer(%q): %v", tt.name, err)
			}
			if s == nil {
				t.Errorf("LookupScorer(%q): returned nil scorer", tt.name)
			}
		}
	}
}

func TestEmptyTrace(t *testing.T) {
	ctx := context.Background()
	s := eval.ExactMatch{}

	r, err := s.Score(ctx, nil, "expected")
	if err != nil {
		t.Fatal(err)
	}
	if r.Score != "fail" {
		t.Errorf("expected fail for empty trace, got %s", r.Score)
	}
}
