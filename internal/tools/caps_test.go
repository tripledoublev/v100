package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTruncateOutput_ShortString(t *testing.T) {
	s := "hello"
	got := TruncateOutput(s, 100)
	if got != s {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestTruncateOutput_ExactLength(t *testing.T) {
	s := strings.Repeat("x", 100)
	got := TruncateOutput(s, 100)
	if got != s {
		t.Errorf("expected unchanged for exact match, got len=%d", len(got))
	}
}

func TestTruncateOutput_Truncated(t *testing.T) {
	s := strings.Repeat("x", 1000)
	got := TruncateOutput(s, 200)
	if len(got) != 200 {
		t.Errorf("expected len 200, got %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation marker, got: %q", got[len(got)-50:])
	}
	if !strings.Contains(got, "800") {
		t.Errorf("expected elided count 800, got: %q", got[len(got)-50:])
	}
}

func TestTruncateOutput_ZeroMax(t *testing.T) {
	s := strings.Repeat("x", 1000)
	got := TruncateOutput(s, 0)
	if got != s {
		t.Errorf("max=0 should disable truncation")
	}
}

func TestTruncateOutput_NegativeMax(t *testing.T) {
	s := strings.Repeat("x", 1000)
	got := TruncateOutput(s, -1)
	if got != s {
		t.Errorf("negative max should disable truncation")
	}
}

func TestTruncateOutput_TinyMax(t *testing.T) {
	// max smaller than suffix length should still work without panicking
	s := strings.Repeat("x", 1000)
	got := TruncateOutput(s, 10)
	if len(got) > 10 {
		t.Errorf("output exceeds max: %d", len(got))
	}
}

func TestCapToolResult_AppliesToOutputAndStdout(t *testing.T) {
	big := strings.Repeat("x", DefaultToolResultChars*2)
	r := ToolResult{OK: true, Output: big, Stdout: big}
	got := CapToolResult(r)
	if len(got.Output) > DefaultToolResultChars {
		t.Errorf("Output not capped: len=%d", len(got.Output))
	}
	if len(got.Stdout) > DefaultToolResultChars {
		t.Errorf("Stdout not capped: len=%d", len(got.Stdout))
	}
	if !strings.Contains(got.Output, "truncated") {
		t.Error("expected truncation marker on Output")
	}
}

func TestCapToolResult_PreservesShort(t *testing.T) {
	r := ToolResult{OK: true, Output: "short", Stdout: "short"}
	got := CapToolResult(r)
	if got.Output != "short" || got.Stdout != "short" {
		t.Errorf("short output should pass through unchanged: %+v", got)
	}
}

func TestDefaultCaps_Sanity(t *testing.T) {
	// Tool-layer chars cap should be slightly above policy default (20000)
	// but well below pathological sizes
	if DefaultToolResultChars < 20000 {
		t.Errorf("DefaultToolResultChars too small: %d", DefaultToolResultChars)
	}
	if DefaultToolResultChars > 100000 {
		t.Errorf("DefaultToolResultChars too large: %d", DefaultToolResultChars)
	}

	// Default fetch bytes should be moderate (not 128KB which was the old value)
	if DefaultFetchBytes < 32*1024 {
		t.Errorf("DefaultFetchBytes too small: %d", DefaultFetchBytes)
	}
	if DefaultFetchBytes > 128*1024 {
		t.Errorf("DefaultFetchBytes too large (regression to old 128KB?): %d", DefaultFetchBytes)
	}

	// MaxFetchBytes should be much larger (caller can request larger)
	if MaxFetchBytes <= DefaultFetchBytes {
		t.Errorf("MaxFetchBytes (%d) should exceed default (%d)", MaxFetchBytes, DefaultFetchBytes)
	}
}

func TestFetchInputSchemasAdvertiseDefaultFetchBytes(t *testing.T) {
	for name, schema := range map[string]json.RawMessage{
		"curl_fetch":  CurlFetch().InputSchema(),
		"web_extract": WebExtract().InputSchema(),
	} {
		var parsed struct {
			Properties map[string]struct {
				Default int64 `json:"default"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Fatalf("%s schema invalid: %v", name, err)
		}
		if got := parsed.Properties["max_bytes"].Default; got != DefaultFetchBytes {
			t.Fatalf("%s max_bytes default = %d, want %d", name, got, DefaultFetchBytes)
		}
	}
}

func TestMarshalShellResultCapsSerializedOutput(t *testing.T) {
	out, stdout, stderr, err := marshalShellResult(strings.Repeat("x", DefaultToolResultChars*3), "", 0)
	if err != nil {
		t.Fatalf("marshalShellResult() error = %v", err)
	}
	if len(out) > DefaultToolResultChars {
		t.Fatalf("serialized output length = %d, want <= %d", len(out), DefaultToolResultChars)
	}
	if len(stdout) > DefaultToolResultChars {
		t.Fatalf("stdout length = %d, want <= %d", len(stdout), DefaultToolResultChars)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("serialized shell output is not valid JSON: %v\n%s", err, out)
	}
}
