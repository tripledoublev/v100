package provenance

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildProvenanceTracksFSWriteLineRangeAndReasoning(t *testing.T) {
	events := []Event{
		testProvEvent(t, "model.response", "s1", "m1", struct {
			Text string `json:"text"`
		}{Text: "write the new file"}),
		testProvEvent(t, "tool.call", "s1", "tc1", struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Args   string `json:"args"`
		}{
			CallID: "call-1",
			Name:   "fs_write",
			Args:   `{"path":"notes.txt","content":"one\ntwo\n"}`,
		}),
		testProvEvent(t, "tool.result", "s1", "tr1", struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Output string `json:"output"`
		}{
			CallID: "call-1",
			Name:   "fs_write",
			OK:     true,
			Output: `{"bytes_written":8,"sha256":"abc"}`,
		}),
	}

	entries := Build(events)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Path != "notes.txt" || entry.LineStart != 1 || entry.LineEnd != 2 {
		t.Fatalf("entry range = %#v, want notes.txt lines 1-2", entry)
	}
	if entry.ToolName != "fs_write" || entry.CallID != "call-1" || entry.EventID != "tr1" {
		t.Fatalf("entry identity = %#v", entry)
	}
	if entry.BytesWritten != 8 || entry.ContentSHA256 != "abc" {
		t.Fatalf("entry content metadata = %#v", entry)
	}
	if entry.ByteStart != 0 || entry.ByteEnd != 8 {
		t.Fatalf("entry byte range = %d-%d, want 0-8", entry.ByteStart, entry.ByteEnd)
	}
	if entry.Reasoning != "write the new file" {
		t.Fatalf("reasoning = %q", entry.Reasoning)
	}
}

func TestBuildProvenanceTracksPatchHunks(t *testing.T) {
	diff := "--- a/a.txt\n+++ b/a.txt\n@@ -2,2 +2,3 @@\n old\n+new\n keep\n"
	events := []Event{
		testProvEvent(t, "tool.call", "s2", "tc2", struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Args   string `json:"args"`
		}{
			CallID: "call-2",
			Name:   "patch_apply",
			Args:   mustProvJSON(t, map[string]string{"diff": diff}),
		}),
		testProvEvent(t, "tool.result", "s2", "tr2", struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Output string `json:"output"`
		}{
			CallID: "call-2",
			Name:   "patch_apply",
			OK:     true,
			Output: "patching file a.txt",
		}),
	}

	entries := Build(events)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].Path != "a.txt" || entries[0].LineStart != 2 || entries[0].LineEnd != 4 {
		t.Fatalf("patch entry = %#v, want a.txt lines 2-4", entries[0])
	}
}

func TestFindProvenanceFiltersByLineNewestFirst(t *testing.T) {
	entries := []ProvenanceEntry{
		{Path: "dir/a.txt", LineStart: 1, LineEnd: 5, CallID: "old"},
		{Path: "dir/a.txt", LineStart: 3, LineEnd: 3, CallID: "new"},
	}
	matches := Find(entries, "a.txt", 3)
	if len(matches) != 2 {
		t.Fatalf("matches len = %d, want 2", len(matches))
	}
	if matches[0].CallID != "new" || matches[1].CallID != "old" {
		t.Fatalf("match order = %#v, want newest first", matches)
	}
	matches = Find(entries, "dir/a.txt", 6)
	if len(matches) != 0 {
		t.Fatalf("line 6 matches len = %d, want 0", len(matches))
	}
}

func testProvEvent(t *testing.T, typ string, stepID, eventID string, payload any) Event {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return Event{
		TS:      time.Unix(1, 0).UTC(),
		RunID:   "run-1",
		StepID:  stepID,
		EventID: eventID,
		Type:    typ,
		Payload: b,
	}
}

func mustProvJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
