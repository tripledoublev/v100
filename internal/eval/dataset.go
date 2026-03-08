package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DatasetItem is a single evaluation sample.
type DatasetItem struct {
	Prompt   string            `json:"prompt"`
	Expected string            `json:"expected,omitempty"`
	Scorer   string            `json:"scorer,omitempty"` // overrides bench-level scorer
	Meta     map[string]string `json:"meta,omitempty"`
}

// Dataset is a collection of evaluation samples.
type Dataset struct {
	Name  string
	Items []DatasetItem
}

// LoadDataset loads a dataset from a JSONL file.
// Each line must be a JSON object with at least a "prompt" field.
func LoadDataset(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("dataset: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	ds := &Dataset{Name: strings.TrimSuffix(path, ".jsonl")}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line limit
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		var item DatasetItem
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("dataset: line %d: %w", lineNum, err)
		}
		if item.Prompt == "" {
			return nil, fmt.Errorf("dataset: line %d: missing prompt field", lineNum)
		}
		ds.Items = append(ds.Items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dataset: scan: %w", err)
	}
	if len(ds.Items) == 0 {
		return nil, fmt.Errorf("dataset: %s: no items found", path)
	}
	return ds, nil
}
