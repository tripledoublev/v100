package tools

import "fmt"

// Default output caps for tools. These are slightly above the policy's
// MaxToolResultChars default (20000) so the policy's truncation layer
// has the final say, but tool-layer caps prevent runaway memory/IO
// from extremely large responses.
const (
	// DefaultToolResultChars is the default character cap for tool outputs.
	// Set slightly above the policy default (20000) to give the loop's
	// truncation logic the final word while preventing pathological cases.
	DefaultToolResultChars = 24000

	// DefaultFetchBytes is the default byte cap for HTTP fetches (curl, web_extract).
	// HTML compresses heavily to text, so we allow more bytes than chars.
	// Was previously 128KB (6x policy default) — now 64KB (~3x policy default).
	DefaultFetchBytes int64 = 64 * 1024

	// MaxFetchBytes is the absolute upper bound on HTTP fetch sizes
	// regardless of caller request. Prevents DoS via large remote responses.
	MaxFetchBytes int64 = 2 * 1024 * 1024
)

// CapToolResult applies DefaultToolResultChars to a ToolResult's Output and
// Stdout fields. Convenience wrapper for tools that build large string/JSON
// outputs and want consistent tool-layer truncation.
func CapToolResult(r ToolResult) ToolResult {
	r.Output = TruncateOutput(r.Output, DefaultToolResultChars)
	r.Stdout = TruncateOutput(r.Stdout, DefaultToolResultChars)
	return r
}

// TruncateOutput truncates s to maxChars characters, appending a
// human-readable suffix indicating how many characters were elided.
// If maxChars <= 0 or s is already short enough, returns s unchanged.
func TruncateOutput(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	elided := len(s) - maxChars
	suffix := fmt.Sprintf("\n…[truncated %d chars]", elided)
	// If suffix alone exceeds the cap, return a truncated suffix
	if len(suffix) >= maxChars {
		return suffix[:maxChars]
	}
	cut := maxChars - len(suffix)
	return s[:cut] + suffix
}
