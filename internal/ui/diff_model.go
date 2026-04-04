package ui

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"

	"github.com/tripledoublev/v100/internal/eval"
)

// DiffModel is a read-only Bubble Tea model for side-by-side trace comparison.
type DiffModel struct {
	width, height   int
	diff            eval.SyncDiff
	leftPane        viewport.Model
	rightPane       viewport.Model
	leftOuterWidth  int
	rightOuterWidth int
	divergeYOffset  int
	ready           bool
}

// NewDiffModel creates a diff viewer for the given synchronized diff.
func NewDiffModel(sd eval.SyncDiff) *DiffModel {
	left := viewport.New(40, 20)
	right := viewport.New(40, 20)
	leftContent, rightContent, divergeLine := buildDiffPaneContents(sd, left.Width, right.Width)
	left.SetContent(leftContent)
	right.SetContent(rightContent)
	return &DiffModel{
		diff:           sd,
		leftPane:       left,
		rightPane:      right,
		divergeYOffset: divergeLine,
	}
}

func (m *DiffModel) Init() tea.Cmd {
	return nil
}

func (m *DiffModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "d":
			if m.divergeYOffset >= 0 {
				m.leftPane.SetYOffset(m.divergeYOffset)
				m.rightPane.SetYOffset(m.divergeYOffset)
			}
			return m, nil
		case "up", "k":
			m.leftPane.ScrollUp(1)
			m.rightPane.ScrollUp(1)
			return m, nil
		case "down", "j":
			m.leftPane.ScrollDown(1)
			m.rightPane.ScrollDown(1)
			return m, nil
		case "pgup":
			m.leftPane.HalfPageUp()
			m.rightPane.HalfPageUp()
			return m, nil
		case "pgdown":
			m.leftPane.HalfPageDown()
			m.rightPane.HalfPageDown()
			return m, nil
		case "home":
			m.leftPane.GotoTop()
			m.rightPane.GotoTop()
			return m, nil
		case "end":
			m.leftPane.GotoBottom()
			m.rightPane.GotoBottom()
			return m, nil
		}
	}
	return m, nil
}

func (m *DiffModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	header := renderDiffHeader(m.diff, m.width)
	footer := renderDiffFooter(m.width)

	leftLabel := tuiHeaderDimStyle.Render("  " + m.diff.RunA)
	rightLabel := tuiHeaderDimStyle.Render("  " + m.diff.RunB)

	leftBox := tuiPaneStyle.Width(m.leftOuterWidth).Render(
		leftLabel + "\n" + m.leftPane.View())
	rightBox := tuiPaneStyle.Width(m.rightOuterWidth).Render(
		rightLabel + "\n" + m.rightPane.View())

	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftBox, " ", rightBox)
	view := lipgloss.JoinVertical(lipgloss.Left, header, panes, footer)
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, view)
}

func (m *DiffModel) recalcLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	headerH := lipgloss.Height(renderDiffHeader(m.diff, m.width))
	footerH := lipgloss.Height(renderDiffFooter(m.width))
	remainingH := max(1, m.height-headerH-footerH)
	viewportHeight := max(1, remainingH-3) // border + label line

	availW := max(2, m.width-1) // keep a 1-column gutter between panes
	m.leftOuterWidth = max(1, availW/2)
	m.rightOuterWidth = max(1, availW-m.leftOuterWidth)

	m.leftPane.Width = max(1, m.leftOuterWidth-2)
	m.rightPane.Width = max(1, m.rightOuterWidth-2)
	m.leftPane.Height = viewportHeight
	m.rightPane.Height = viewportHeight

	leftContent, rightContent, divergeLine := buildDiffPaneContents(
		m.diff,
		m.leftPane.Width,
		m.rightPane.Width,
	)
	m.leftPane.SetContent(leftContent)
	m.rightPane.SetContent(rightContent)
	m.divergeYOffset = divergeLine
}
