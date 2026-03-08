package eval_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/eval"
)

func TestLoadDataset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	lines := `{"prompt": "What is 2+2?", "expected": "4", "scorer": "contains"}
{"prompt": "Name a color", "expected": "blue"}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ds, err := eval.LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(ds.Items))
	}
	if ds.Items[0].Prompt != "What is 2+2?" {
		t.Errorf("unexpected prompt: %s", ds.Items[0].Prompt)
	}
	if ds.Items[0].Scorer != "contains" {
		t.Errorf("unexpected scorer: %s", ds.Items[0].Scorer)
	}
	if ds.Items[1].Expected != "blue" {
		t.Errorf("unexpected expected: %s", ds.Items[1].Expected)
	}
}

func TestLoadDatasetSkipsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	lines := `# comment line
{"prompt": "test"}
// another comment

`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ds, err := eval.LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(ds.Items))
	}
}

func TestLoadDatasetEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := eval.LoadDataset(path)
	if err == nil {
		t.Error("expected error for empty dataset")
	}
}

func TestLoadDatasetMissingPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(path, []byte(`{"expected": "test"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := eval.LoadDataset(path)
	if err == nil {
		t.Error("expected error for missing prompt")
	}
}
