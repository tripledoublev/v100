package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tripledoublev/v100/internal/core"
)

func writeTraceEvent(t *testing.T, trace *core.TraceWriter, runID, stepID string, typ core.EventType, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Write(core.Event{
		TS:      time.Now().UTC(),
		RunID:   runID,
		StepID:  stepID,
		EventID: "ev-" + string(typ),
		Type:    typ,
		Payload: raw,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMaybePrintFailureDigestOnError(t *testing.T) {
	trace, err := core.OpenTrace(t.TempDir() + "/trace.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	writeTraceEvent(t, trace, "run-err", "step-1", core.EventRunStart, core.RunStartPayload{})
	writeTraceEvent(t, trace, "run-err", "step-1", core.EventRunError, core.RunErrorPayload{Error: "Error: provider stream: timeout\nstack traceback:"})
	writeTraceEvent(t, trace, "run-err", "step-1", core.EventRunEnd, core.RunEndPayload{Reason: "error"})

	var buf bytes.Buffer
	maybePrintFailureDigest(&buf, trace.Path(), "error")
	got := buf.String()
	if !strings.Contains(got, "Failure Digest") {
		t.Fatalf("expected digest output, got:\n%s", got)
	}
	if !strings.Contains(got, "provider stream: timeout") {
		t.Fatalf("expected compact cause in digest, got:\n%s", got)
	}
}

func TestMaybePrintFailureDigestSkipsNonFailure(t *testing.T) {
	trace, err := core.OpenTrace(t.TempDir() + "/trace.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trace.Close() }()

	writeTraceEvent(t, trace, "run-ok", "step-1", core.EventRunStart, core.RunStartPayload{})
	writeTraceEvent(t, trace, "run-ok", "step-1", core.EventRunEnd, core.RunEndPayload{Reason: "completed"})

	var buf bytes.Buffer
	maybePrintFailureDigest(&buf, trace.Path(), "completed")
	if buf.Len() != 0 {
		t.Fatalf("expected no digest output, got:\n%s", buf.String())
	}
}
