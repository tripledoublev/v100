package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func PersistCheckpoint(runDir string, cp Checkpoint) error {
	if strings.TrimSpace(runDir) == "" {
		return fmt.Errorf("checkpoint store: run directory is required")
	}
	if strings.TrimSpace(cp.ID) == "" {
		return fmt.Errorf("checkpoint store: checkpoint id is required")
	}
	dir := checkpointDir(runDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("checkpoint store: mkdir: %w", err)
	}
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("checkpoint store: marshal: %w", err)
	}
	path := checkpointPath(runDir, cp.ID)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("checkpoint store: write %s: %w", path, err)
	}
	return nil
}

func ReadCheckpoint(runDir, id string) (Checkpoint, error) {
	if strings.TrimSpace(runDir) == "" {
		return Checkpoint{}, fmt.Errorf("checkpoint store: run directory is required")
	}
	if strings.TrimSpace(id) == "" {
		return Checkpoint{}, fmt.Errorf("checkpoint store: checkpoint id is required")
	}
	b, err := os.ReadFile(checkpointPath(runDir, id))
	if err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint store: read %s: %w", id, err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return Checkpoint{}, fmt.Errorf("checkpoint store: parse %s: %w", id, err)
	}
	if cp.ID == "" {
		cp.ID = id
	}
	return cp, nil
}

func ListCheckpoints(runDir string) ([]Checkpoint, error) {
	dir := checkpointDir(runDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint store: read dir: %w", err)
	}
	var out []Checkpoint
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		cp, err := ReadCheckpoint(runDir, id)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func LatestCheckpoint(runDir string) (Checkpoint, error) {
	checkpoints, err := ListCheckpoints(runDir)
	if err != nil {
		return Checkpoint{}, err
	}
	if len(checkpoints) == 0 {
		return Checkpoint{}, fmt.Errorf("checkpoint store: no checkpoints found")
	}
	return checkpoints[len(checkpoints)-1], nil
}

func checkpointDir(runDir string) string {
	return filepath.Join(runDir, "checkpoints")
}

func checkpointPath(runDir, id string) string {
	return filepath.Join(checkpointDir(runDir), sanitizeCheckpointID(id)+".json")
}

func sanitizeCheckpointID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "/", "-")
	id = strings.ReplaceAll(id, "\\", "-")
	if id == "" {
		return "checkpoint"
	}
	return id
}
