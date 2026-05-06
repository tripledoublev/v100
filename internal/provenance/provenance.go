package provenance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Event is the subset of a v100 trace event needed for provenance analysis.
type Event struct {
	TS      time.Time       `json:"ts"`
	RunID   string          `json:"run_id"`
	StepID  string          `json:"step_id"`
	EventID string          `json:"event_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// ProvenanceEntry records which trace event produced a line range in a file.
type ProvenanceEntry struct {
	Path          string    `json:"path"`
	LineStart     int       `json:"line_start"`
	LineEnd       int       `json:"line_end"`
	ByteStart     int       `json:"byte_start,omitempty"`
	ByteEnd       int       `json:"byte_end,omitempty"`
	ToolName      string    `json:"tool_name"`
	CallID        string    `json:"call_id"`
	StepID        string    `json:"step_id"`
	EventID       string    `json:"event_id"`
	TS            time.Time `json:"ts,omitempty"`
	BytesWritten  int       `json:"bytes_written,omitempty"`
	ContentSHA256 string    `json:"content_sha256,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Reasoning     string    `json:"reasoning,omitempty"`
}

// Build derives line-level custody records from mutating tool calls in
// a trace. It is intentionally trace-only: callers can persist the returned
// records as an artifact, render them in the CLI, or expose them through a tool.
func ReadAll(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("provenance: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("provenance: parse line %d: %w", lineNum, err)
		}
		events = append(events, ev)
	}
	return events, scanner.Err()
}

func Build(events []Event) []ProvenanceEntry {
	type callDetail struct {
		Name      string
		Path      string
		Content   string
		Diff      string
		LineStart int
		LineEnd   int
		SHA256    string
		Summary   string
	}

	calls := make(map[string]callDetail)
	latestReasoningByStep := make(map[string]string)
	for _, ev := range events {
		switch ev.Type {
		case "model.response":
			var payload struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(ev.Payload, &payload) == nil && strings.TrimSpace(payload.Text) != "" {
				latestReasoningByStep[ev.StepID] = strings.TrimSpace(payload.Text)
			}
		case "tool.call":
			var payload struct {
				CallID string `json:"call_id"`
				Name   string `json:"name"`
				Args   string `json:"args"`
			}
			if json.Unmarshal(ev.Payload, &payload) != nil {
				continue
			}
			switch payload.Name {
			case "fs_write":
				var args struct {
					Path    string `json:"path"`
					Content string `json:"content"`
					Append  bool   `json:"append"`
				}
				if json.Unmarshal([]byte(payload.Args), &args) != nil || strings.TrimSpace(args.Path) == "" {
					continue
				}
				start, end := 1, countLines(args.Content)
				if end == 0 {
					end = 1
				}
				summary := "overwrite"
				if args.Append {
					start, end = 0, 0
					summary = "append; exact final line range depends on preexisting file length"
				}
				calls[payload.CallID] = callDetail{
					Name:      payload.Name,
					Path:      cleanProvenancePath(args.Path),
					Content:   args.Content,
					LineStart: start,
					LineEnd:   end,
					Summary:   summary,
				}
			case "patch_apply":
				var args struct {
					Diff string `json:"diff"`
				}
				if json.Unmarshal([]byte(payload.Args), &args) != nil || strings.TrimSpace(args.Diff) == "" {
					continue
				}
				calls[payload.CallID] = callDetail{Name: payload.Name, Diff: args.Diff}
			}
		}
	}

	var entries []ProvenanceEntry
	for _, ev := range events {
		if ev.Type != "tool.result" {
			continue
		}
		var payload struct {
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Output string `json:"output"`
		}
		if json.Unmarshal(ev.Payload, &payload) != nil || !payload.OK {
			continue
		}
		detail, ok := calls[payload.CallID]
		if !ok {
			continue
		}
		reasoning := latestReasoningByStep[ev.StepID]
		switch payload.Name {
		case "fs_write":
			bytesWritten, sha := parseFSWriteOutput(payload.Output)
			if detail.LineStart == 0 && detail.LineEnd == 0 {
				entries = append(entries, ProvenanceEntry{
					Path:          detail.Path,
					ToolName:      payload.Name,
					CallID:        payload.CallID,
					StepID:        ev.StepID,
					EventID:       ev.EventID,
					TS:            ev.TS,
					BytesWritten:  bytesWritten,
					ByteEnd:       bytesWritten,
					ContentSHA256: sha,
					Summary:       detail.Summary,
					Reasoning:     reasoning,
				})
				continue
			}
			entries = append(entries, ProvenanceEntry{
				Path:          detail.Path,
				LineStart:     detail.LineStart,
				LineEnd:       detail.LineEnd,
				ByteEnd:       bytesWritten,
				ToolName:      payload.Name,
				CallID:        payload.CallID,
				StepID:        ev.StepID,
				EventID:       ev.EventID,
				TS:            ev.TS,
				BytesWritten:  bytesWritten,
				ContentSHA256: sha,
				Summary:       detail.Summary,
				Reasoning:     reasoning,
			})
		case "patch_apply":
			for _, hunk := range parsePatchLineRanges(detail.Diff) {
				entries = append(entries, ProvenanceEntry{
					Path:      hunk.path,
					LineStart: hunk.start,
					LineEnd:   hunk.end,
					ToolName:  payload.Name,
					CallID:    payload.CallID,
					StepID:    ev.StepID,
					EventID:   ev.EventID,
					TS:        ev.TS,
					Summary:   "patch hunk",
					Reasoning: reasoning,
				})
			}
		}
	}
	return entries
}

// Find returns entries that apply to path and, when line > 0, contain
// that line. Later entries are returned first because they are the effective
// custody for overlapping edits.
func Find(entries []ProvenanceEntry, path string, line int) []ProvenanceEntry {
	target := cleanProvenancePath(path)
	targetBase := filepath.Base(target)
	var matches []ProvenanceEntry
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !provenancePathMatches(entry.Path, target, targetBase) {
			continue
		}
		if line > 0 && (entry.LineStart == 0 || line < entry.LineStart || line > entry.LineEnd) {
			continue
		}
		matches = append(matches, entry)
	}
	return matches
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	lines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return lines
}

func parseFSWriteOutput(output string) (int, string) {
	var parsed struct {
		BytesWritten int    `json:"bytes_written"`
		SHA256       string `json:"sha256"`
	}
	if json.Unmarshal([]byte(output), &parsed) == nil {
		return parsed.BytesWritten, parsed.SHA256
	}
	return 0, ""
}

type patchRange struct {
	path       string
	start, end int
}

var patchHunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func parsePatchLineRanges(diff string) []patchRange {
	var ranges []patchRange
	var path string
	for _, raw := range strings.Split(diff, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(line, "+++ ") {
			path = cleanPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			continue
		}
		match := patchHunkRe.FindStringSubmatch(line)
		if match == nil || path == "" || path == "/dev/null" {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		end := start
		if count > 0 {
			end = start + count - 1
		}
		ranges = append(ranges, patchRange{path: path, start: start, end: end})
	}
	return ranges
}

func cleanPatchPath(path string) string {
	path = strings.TrimSpace(path)
	if fields := strings.Fields(path); len(fields) > 0 {
		path = fields[0]
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return cleanProvenancePath(path)
}

func cleanProvenancePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func provenancePathMatches(entryPath, targetPath, targetBase string) bool {
	entryPath = cleanProvenancePath(entryPath)
	targetPath = cleanProvenancePath(targetPath)
	if entryPath == targetPath {
		return true
	}
	if !strings.Contains(targetPath, "/") {
		return filepath.Base(entryPath) == targetBase
	}
	return strings.HasSuffix(entryPath, "/"+targetPath)
}

func FormatProvenanceEntry(entry ProvenanceEntry) string {
	lineRange := "line range unknown"
	if entry.LineStart > 0 {
		lineRange = fmt.Sprintf("lines %d-%d", entry.LineStart, entry.LineEnd)
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "%s %s via %s call %s", entry.Path, lineRange, entry.ToolName, entry.CallID)
	if entry.EventID != "" {
		_, _ = fmt.Fprintf(&b, " event %s", entry.EventID)
	}
	if strings.TrimSpace(entry.Reasoning) != "" {
		_, _ = fmt.Fprintf(&b, "\nreasoning: %s", strings.TrimSpace(entry.Reasoning))
	}
	return b.String()
}
