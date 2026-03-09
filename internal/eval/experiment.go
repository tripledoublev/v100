package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Experiment defines a set of trials to test specific hypothesis (e.g., Solver A vs B).
type Experiment struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	CreatedAt time.Time        `json:"created_at"`
	Config    ExperimentConfig `json:"config"`
	Status    string           `json:"status"` // pending, running, completed
	RunIDs    []string         `json:"run_ids"`
}

// ExperimentConfig defines the dataset and variants for an experiment.
type ExperimentConfig struct {
	DatasetPath string             `json:"dataset_path,omitempty"`
	Variants    []Variant          `json:"variants"`
	Repeats     int                `json:"repeats"` // N trials per prompt/variant combination
	Scorer      string             `json:"scorer"`
	Invariants  []SuccessInvariant `json:"invariants,omitempty"`
}

// SuccessInvariant defines a physical condition that must be true for a run to "pass".
type SuccessInvariant struct {
	Type    string `json:"type"` // "file_exists", "file_contains", "file_sha256", "no_file"
	Path    string `json:"path"` // path relative to /workspace
	Pattern string `json:"pattern,omitempty"`
	Hash    string `json:"hash,omitempty"`
}

// Variant is a specific combination of model, solver, and parameters.
type Variant struct {
	Name     string         `json:"name"`
	Model    string         `json:"model,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Solver   string         `json:"solver,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// NewExperiment creates a new research experiment.
func NewExperiment(name string, cfg ExperimentConfig) *Experiment {
	id := fmt.Sprintf("exp-%s-%d", name, time.Now().Unix())
	return &Experiment{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now(),
		Config:    cfg,
		Status:    "pending",
		RunIDs:    []string{},
	}
}

// Save persists the experiment metadata to disk.
func (e *Experiment) Save(baseDir string) error {
	path := filepath.Join(baseDir, "experiments", e.ID, "experiment.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadExperiment reads an experiment from disk.
func LoadExperiment(baseDir, id string) (*Experiment, error) {
	path := filepath.Join(baseDir, "experiments", id, "experiment.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Experiment
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}
