package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// TraceWriter writes append-only JSONL trace events to disk.
type TraceWriter struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// OpenTrace opens (or creates) a trace file at the given path.
// The parent directory must already exist.
func OpenTrace(path string) (*TraceWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("trace: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("trace: open %s: %w", path, err)
	}
	return &TraceWriter{f: f, path: path}, nil
}

// Write marshals the event and appends it as a newline-delimited JSON record.
func (tw *TraceWriter) Write(event Event) error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	b, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("trace: marshal: %w", err)
	}
	b = append(b, '\n')
	if _, err := tw.f.Write(b); err != nil {
		return fmt.Errorf("trace: write: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file.
func (tw *TraceWriter) Close() error {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return tw.f.Close()
}

// Path returns the file path of this trace.
func (tw *TraceWriter) Path() string {
	return tw.path
}

// ReadAll reads all events from a JSONL trace file.
func ReadAll(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("trace: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1 MB max line
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("trace: parse line %d: %w", line, err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("trace: scan: %w", err)
	}
	return events, nil
}
