package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

type listedRun struct {
	ID       string
	Dir      string
	Provider string
	Model    string
	Name     string
	Prompt   string
	SubRun   bool
}

var execResumeRun = defaultExecResumeRun

var canonicalRunDirPattern = regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{8}$`)

func runsCmd(cfgPath *string) *cobra.Command {
	var limit int
	var runDir string
	var allFlag bool
	var providerFilter string
	var failedFlag bool
	var plainFlag bool

	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List recent runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if runDir == "" {
				runDir = "runs"
			}

			runs, err := collectRuns(runDir, allFlag, providerFilter, failedFlag)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println(ui.Dim("No runs found"))
					return nil
				}
				return err
			}
			if limit > 0 && len(runs) > limit {
				runs = runs[:limit]
			}
			if len(runs) == 0 {
				fmt.Println(ui.Dim("No runs found"))
				return nil
			}

			if !plainFlag && interactiveRunsTTY() {
				selected, err := pickRunToResume(runs)
				if err != nil {
					return err
				}
				if selected == "" {
					return nil
				}
				configPath := ""
				if cfgPath != nil {
					configPath = strings.TrimSpace(*cfgPath)
				}
				return execResumeRun(configPath, selected)
			}

			for _, run := range runs {
				fmt.Println(formatRun(run))
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "max runs to show")
	cmd.Flags().StringVar(&runDir, "run-dir", "", "runs directory (default: ./runs)")
	cmd.Flags().BoolVar(&allFlag, "all", false, "show sub-runs and all entries")
	cmd.Flags().StringVar(&providerFilter, "provider", "", "filter by provider name")
	cmd.Flags().BoolVar(&failedFlag, "failed", false, "show only failed/errored runs")
	cmd.Flags().BoolVar(&plainFlag, "plain", false, "print plain text list instead of interactive picker")
	return cmd
}

func interactiveRunsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func defaultExecResumeRun(configPath, runID string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	args := []string{}
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", configPath)
	}
	args = append(args, "resume", runID)
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func collectRuns(runDir string, allFlag bool, providerFilter string, failedFlag bool) ([]listedRun, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return nil, err
	}

	type runEntry struct {
		name    string
		modTime time.Time
	}
	var dirs []runEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, runEntry{name: e.Name(), modTime: info.ModTime()})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].modTime.After(dirs[j].modTime)
	})

	runs := make([]listedRun, 0, len(dirs))
	for _, d := range dirs {
		dir := filepath.Join(runDir, d.name)
		if !allFlag && !isPrimaryRunDir(dir, d.name) {
			continue
		}
		meta, _ := core.ReadMeta(dir)

		if !allFlag && meta.ParentRunID != "" {
			continue
		}
		if providerFilter != "" && meta.Provider != providerFilter {
			continue
		}
		if failedFlag {
			events, _ := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
			stats := core.ComputeStats(events)
			if stats.EndReason == "completed" && meta.Score != "fail" {
				continue
			}
		}

		runs = append(runs, listedRun{
			ID:       d.name,
			Dir:      dir,
			Provider: strings.TrimSpace(meta.Provider),
			Model:    strings.TrimSpace(meta.Model),
			Name:     strings.TrimSpace(meta.Name),
			Prompt:   firstUserPrompt(dir),
			SubRun:   meta.ParentRunID != "",
		})
	}
	return runs, nil
}

func isPrimaryRunDir(dir, name string) bool {
	if !canonicalRunDirPattern.MatchString(name) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "trace.jsonl")); err != nil {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return false
	}
	return true
}

func formatRun(run listedRun) string {
	label := run.ID
	if run.SubRun {
		label = "  ↳ " + run.ID
	}
	parts := []string{}
	if run.Provider != "" {
		parts = append(parts, run.Provider)
	}
	if run.Model != "" {
		parts = append(parts, run.Model)
	}
	if run.Name != "" {
		parts = append(parts, ui.Bold(run.Name))
	}
	if len(parts) > 0 {
		label += "  " + strings.Join(parts, " · ")
	}
	if run.Prompt != "" {
		label += "\n    " + ui.Dim(run.Prompt)
	}
	return label
}

type runPickerModel struct {
	items    []listedRun
	cursor   int
	offset   int
	width    int
	height   int
	selected string
}

func pickRunToResume(items []listedRun) (string, error) {
	model := &runPickerModel{
		items:  items,
		width:  100,
		height: 24,
	}
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}
	result, _ := finalModel.(*runPickerModel)
	if result == nil {
		return "", nil
	}
	return result.selected, nil
}

func (m *runPickerModel) Init() tea.Cmd {
	return tea.Batch(tea.WindowSize(), tea.EnableMouseCellMotion)
}

func (m *runPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			return m, tea.Quit
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-m.pageSize())
		case "pgdown":
			m.move(m.pageSize())
		case "home":
			m.cursor = 0
			m.adjustOffset()
		case "end":
			m.cursor = len(m.items) - 1
			m.adjustOffset()
		case "enter":
			if len(m.items) > 0 {
				m.selected = m.items[m.cursor].ID
			}
			return m, tea.Quit
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.move(-1)
		case tea.MouseButtonWheelDown:
			m.move(1)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress {
				idx, ok := m.indexForMouseY(msg.Y)
				if ok {
					m.cursor = idx
					m.selected = m.items[idx].ID
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m *runPickerModel) View() string {
	if len(m.items) == 0 {
		return "No runs found"
	}

	lines := []string{
		ui.Bold("Recent Runs"),
		ui.Dim("Click a run or press Enter to resume. q to cancel."),
		"",
	}
	for _, idx := range m.visibleIndices() {
		lines = append(lines, m.renderItem(idx)...)
	}
	return strings.Join(lines, "\n")
}

func (m *runPickerModel) move(delta int) {
	if len(m.items) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	m.adjustOffset()
}

func (m *runPickerModel) adjustOffset() {
	page := m.pageSize()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+page {
		m.offset = m.cursor - page + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *runPickerModel) pageSize() int {
	usable := m.height - 3
	if usable < 3 {
		return 1
	}
	page := usable / 3
	if page < 1 {
		page = 1
	}
	return page
}

func (m *runPickerModel) visibleIndices() []int {
	page := m.pageSize()
	end := m.offset + page
	if end > len(m.items) {
		end = len(m.items)
	}
	indices := make([]int, 0, end-m.offset)
	for i := m.offset; i < end; i++ {
		indices = append(indices, i)
	}
	return indices
}

func (m *runPickerModel) renderItem(idx int) []string {
	item := m.items[idx]
	prefix := "  "
	if idx == m.cursor {
		prefix = "> "
	}

	label := item.ID
	if item.SubRun {
		label = "↳ " + item.ID
	}
	parts := []string{}
	if item.Provider != "" {
		parts = append(parts, item.Provider)
	}
	if item.Model != "" {
		parts = append(parts, item.Model)
	}
	if item.Name != "" {
		parts = append(parts, item.Name)
	}
	line1 := prefix + truncateRunes(label+"  "+strings.Join(parts, " · "), max(20, m.width-2))

	prompt := item.Prompt
	if prompt == "" {
		prompt = "(no prompt captured)"
	}
	line2 := "   " + truncateRunes(prompt, max(20, m.width-3))
	if idx == m.cursor {
		line2 = "   " + ui.Dim(truncateRunes(prompt, max(20, m.width-3)))
	}
	return []string{line1, line2, ""}
}

func (m *runPickerModel) indexForMouseY(y int) (int, bool) {
	if y < 3 {
		return 0, false
	}
	row := y - 3
	idx := m.offset + (row / 3)
	if idx < 0 || idx >= len(m.items) {
		return 0, false
	}
	return idx, true
}

func truncateRunes(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	return string(runes[:maxWidth-1]) + "…"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// firstUserPrompt reads the trace and returns the first user message, truncated.
func firstUserPrompt(dir string) string {
	events, err := core.ReadAll(filepath.Join(dir, "trace.jsonl"))
	if err != nil {
		return ""
	}
	for _, ev := range events {
		if ev.Type != core.EventUserMsg {
			continue
		}
		var p core.UserMsgPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			continue
		}
		prompt := strings.TrimSpace(p.Content)
		prompt = strings.Join(strings.Fields(prompt), " ")
		if len(prompt) > 80 {
			prompt = prompt[:77] + "..."
		}
		return prompt
	}
	return ""
}
