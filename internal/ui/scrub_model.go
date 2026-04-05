package ui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/core"
)

type scrubFocus int

const (
	scrubFocusList scrubFocus = iota
	scrubFocusDetail
)

func (f scrubFocus) String() string {
	switch f {
	case scrubFocusDetail:
		return "detail"
	default:
		return "list"
	}
}

// ScrubModel is an interactive Bubble Tea model for stepping through a run trace.
type ScrubModel struct {
	width, height int
	events        []core.Event
	runID         string
	cursor        int
	listPane      viewport.Model
	detailPane    viewport.Model
	focus         scrubFocus
	cursorStart   int
	cursorEnd     int
	ready         bool
}

func NewScrubModel(runID string, events []core.Event) *ScrubModel {
	return &ScrubModel{
		events:     append([]core.Event(nil), events...),
		runID:      runID,
		listPane:   viewport.New(60, 16),
		detailPane: viewport.New(60, 10),
		focus:      scrubFocusList,
	}
}

func (m *ScrubModel) Init() tea.Cmd {
	return nil
}

func (m *ScrubModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recalcLayout()
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focus == scrubFocusList {
				m.focus = scrubFocusDetail
			} else {
				m.focus = scrubFocusList
			}
			m.recalcLayout()
			return m, nil
		case "s":
			m.jumpToNextStepSummary()
			return m, nil
		}

		if m.focus == scrubFocusDetail {
			m.scrollDetail(msg.String())
			return m, nil
		}
		m.navigateList(msg.String())
		return m, nil
	}

	return m, nil
}

func (m *ScrubModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	header := renderScrubHeader(m.runID, m.cursor, len(m.events), m.currentEvent(), m.focus, m.width)
	footer := renderScrubFooter(m.width, m.focus)

	listStyle := tuiPaneStyle
	detailStyle := tuiPaneStyle
	if m.focus == scrubFocusList {
		listStyle = tuiActivePaneStyle
	} else {
		detailStyle = tuiActivePaneStyle
	}

	listBox := listStyle.Width(m.width).Render(
		tuiHeaderDimStyle.Render("  events") + "\n" + m.listPane.View(),
	)
	detailBox := detailStyle.Width(m.width).Render(
		tuiHeaderDimStyle.Render("  detail") + "\n" + m.detailPane.View(),
	)

	view := lipgloss.JoinVertical(lipgloss.Left, header, listBox, detailBox, footer)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
}

func (m *ScrubModel) recalcLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	headerH := lipgloss.Height(renderScrubHeader(m.runID, m.cursor, len(m.events), m.currentEvent(), m.focus, m.width))
	footerH := lipgloss.Height(renderScrubFooter(m.width, m.focus))
	remainingH := max(2, m.height-headerH-footerH)

	listOuterH := max(4, remainingH*3/5)
	detailOuterH := max(4, remainingH-listOuterH)
	if remainingH >= 8 && detailOuterH < 4 {
		detailOuterH = 4
		listOuterH = max(4, remainingH-detailOuterH)
	}
	if remainingH >= 8 && listOuterH < 4 {
		listOuterH = 4
		detailOuterH = max(4, remainingH-listOuterH)
	}

	m.listPane.Width = max(1, m.width-2)
	m.detailPane.Width = max(1, m.width-2)
	m.listPane.Height = max(1, listOuterH-3)
	m.detailPane.Height = max(1, detailOuterH-3)
	m.refreshContent(false)
}

func (m *ScrubModel) navigateList(key string) {
	switch key {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-10)
	case "pgdown":
		m.moveCursor(10)
	case "home":
		m.setCursor(0)
	case "end":
		m.setCursor(len(m.events) - 1)
	}
}

func (m *ScrubModel) scrollDetail(key string) {
	switch key {
	case "up", "k":
		m.detailPane.ScrollUp(1)
	case "down", "j":
		m.detailPane.ScrollDown(1)
	case "pgup":
		m.detailPane.HalfPageUp()
	case "pgdown":
		m.detailPane.HalfPageDown()
	case "home":
		m.detailPane.GotoTop()
	case "end":
		m.detailPane.GotoBottom()
	}
}

func (m *ScrubModel) moveCursor(delta int) {
	m.setCursor(m.cursor + delta)
}

func (m *ScrubModel) setCursor(idx int) {
	if len(m.events) == 0 {
		m.cursor = 0
		m.refreshContent(true)
		return
	}
	idx = clampScrubCursor(idx, len(m.events))
	if idx == m.cursor && m.detailPane.TotalLineCount() > 0 {
		m.ensureCursorVisible()
		return
	}
	m.cursor = idx
	m.refreshContent(true)
}

func (m *ScrubModel) jumpToNextStepSummary() {
	for idx := m.cursor + 1; idx < len(m.events); idx++ {
		if m.events[idx].Type == core.EventStepSummary {
			m.setCursor(idx)
			return
		}
	}
}

func (m *ScrubModel) refreshContent(resetDetail bool) {
	listContent, cursorStart, cursorEnd := buildScrubListContent(m.events, m.cursor, m.listPane.Width)
	m.listPane.SetContent(listContent)
	m.cursorStart = cursorStart
	m.cursorEnd = cursorEnd
	m.ensureCursorVisible()

	m.detailPane.SetContent(buildScrubDetailContent(m.currentEvent(), m.detailPane.Width))
	if resetDetail {
		m.detailPane.GotoTop()
	}
}

func (m *ScrubModel) ensureCursorVisible() {
	if m.cursorEnd-m.cursorStart+1 >= max(1, m.listPane.Height) {
		m.listPane.SetYOffset(m.cursorStart)
		return
	}
	if m.cursorStart < m.listPane.YOffset {
		m.listPane.SetYOffset(m.cursorStart)
		return
	}
	bottom := m.listPane.YOffset + max(1, m.listPane.Height) - 1
	if m.cursorEnd > bottom {
		m.listPane.SetYOffset(m.cursorEnd - max(1, m.listPane.Height) + 1)
	}
}

func (m *ScrubModel) currentEvent() *core.Event {
	if len(m.events) == 0 {
		return nil
	}
	idx := clampScrubCursor(m.cursor, len(m.events))
	return &m.events[idx]
}

func clampScrubCursor(idx, total int) int {
	if total <= 0 {
		return 0
	}
	if idx < 0 {
		return 0
	}
	if idx >= total {
		return total - 1
	}
	return idx
}
