package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HistoryEntry represents a single bench run's historical record.
type HistoryEntry struct {
	RunID     string            `json:"run_id"`
	Name      string            `json:"name"`
	Variant   string            `json:"variant"`
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	Score     string            `json:"score"`
	ScoreNotes string           `json:"score_notes,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// LoadHistory scans a runs directory for all runs matching a bench name
// and returns them sorted by CreatedAt ascending.
func LoadHistory(runsDir, benchName string) ([]HistoryEntry, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil, fmt.Errorf("read runs dir: %w", err)
	}

	var results []HistoryEntry

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(runsDir, entry.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}

		// Use a flexible struct to parse only what we need
		var raw struct {
			Name       string            `json:"name"`
			RunID      string            `json:"run_id"`
			Tags       map[string]string `json:"tags"`
			Provider   string            `json:"provider"`
			Model      string            `json:"model"`
			Score      string            `json:"score"`
			ScoreNotes string            `json:"score_notes"`
			CreatedAt  time.Time         `json:"created_at"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}

		// Filter by bench name
		if raw.Name != benchName {
			// Also check tags
			if raw.Tags == nil || raw.Tags["experiment"] != benchName {
				continue
			}
		}

		variant := ""
		if raw.Tags != nil {
			variant = raw.Tags["variant"]
		}

		results = append(results, HistoryEntry{
			RunID:      raw.RunID,
			Name:       raw.Name,
			Variant:    variant,
			Provider:   raw.Provider,
			Model:      raw.Model,
			Score:      raw.Score,
			ScoreNotes: raw.ScoreNotes,
			Tags:       raw.Tags,
			CreatedAt:  raw.CreatedAt,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.Before(results[j].CreatedAt)
	})

	return results, nil
}

// ScoreToNumeric converts a score string to a numeric value for sparklines.
// pass=1, partial=0.5, fail=0, unknown=-1.
func ScoreToNumeric(score string) float64 {
	switch strings.ToLower(strings.TrimSpace(score)) {
	case "pass":
		return 1
	case "partial":
		return 0.5
	case "fail":
		return 0
	default:
		return -1
	}
}

// Sparkline renders an ASCII sparkline from numeric values.
// Uses block characters for visual representation.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}

	const (
		blockFull  = "█"
		blockHalf  = "▄"
		blockEmpty = "░"
		blockNone  = "·"
	)

	var sb strings.Builder
	for _, v := range values {
		switch {
		case v < 0:
			sb.WriteString(blockNone)
		case v >= 1:
			sb.WriteString(blockFull)
		case v >= 0.5:
			sb.WriteString(blockHalf)
		default:
			sb.WriteString(blockEmpty)
		}
	}
	return sb.String()
}

// FormatHistoryTable formats history entries as a readable table.
func FormatHistoryTable(entries []HistoryEntry) string {
	if len(entries) == 0 {
		return "No history found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%-28s  %-10s %-10s %-12s %-8s %-20s\n",
		"RUN ID", "PROVIDER", "MODEL", "VARIANT", "SCORE", "TIMESTAMP"))
	sb.WriteString(strings.Repeat("─", 95))
	sb.WriteString("\n")

	for _, e := range entries {
		score := e.Score
		if score == "" {
			score = "-"
		}
		ts := e.CreatedAt.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("%-28s  %-10s %-10s %-12s %-8s %-20s\n",
			e.RunID, e.Provider, e.Model, e.Variant, score, ts))
	}

	return sb.String()
}

// FormatTrendSummary produces a compact trend view with sparkline.
func FormatTrendSummary(entries []HistoryEntry) string {
	if len(entries) == 0 {
		return "No history found."
	}

	var values []float64
	passCount := 0
	failCount := 0

	for _, e := range entries {
		v := ScoreToNumeric(e.Score)
		values = append(values, v)
		if v >= 1 {
			passCount++
		} else if v == 0 {
			failCount++
		}
	}

	total := len(entries)
	passRate := float64(passCount) / float64(total) * 100

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Bench: %s\n", entries[0].Name))
	sb.WriteString(fmt.Sprintf("Runs:  %d  |  Pass: %d  |  Fail: %d  |  Pass Rate: %.0f%%\n",
		total, passCount, failCount, passRate))
	sb.WriteString(fmt.Sprintf("Trend: %s\n", Sparkline(values)))

	// Show last 5 results detail
	sb.WriteString("\nRecent runs:\n")
	start := 0
	if len(entries) > 5 {
		start = len(entries) - 5
	}
	for i := start; i < len(entries); i++ {
		e := entries[i]
		score := strings.ToUpper(e.Score)
		if score == "" {
			score = "?"
		}
		notes := ""
		if e.ScoreNotes != "" {
			notes = " — " + e.ScoreNotes
			if len(notes) > 60 {
				notes = notes[:57] + "..."
			}
		}
		sb.WriteString(fmt.Sprintf("  %s  %s  %s/%s%s\n",
			e.CreatedAt.Format("Jan 02 15:04"),
			score,
			e.Provider,
			e.Model,
			notes,
		))
	}

	return sb.String()
}
