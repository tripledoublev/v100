package ui

import (
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	lipgloss "github.com/charmbracelet/lipgloss"
	"github.com/tripledoublev/v100/internal/core"
)

// focus identifies which pane is active.
type focus int

const (
	focusInput focus = iota
	focusTranscript
	focusTrace
	focusStatus
)

// confirmState holds pending confirmation data.
type confirmState struct {
	active   bool
	toolName string
	args     string
	approved chan bool
}

// copyTarget records a copy-icon line and its associated content.
type copyTarget struct {
	lineNo  int
	content string
}

type agentFrame struct {
	RunID    string
	CallID   string
	Task     string
	Model    string
	MaxSteps int
	Tools    int
	Started  time.Time
}

type TranscriptItemType int

const (
	ItemMessage TranscriptItemType = iota
	ItemWelcome
	ItemImage
	ItemToolGroup
	ItemAgentStart
	ItemAgentEnd
	ItemRunEnd
	ItemTokenGroup
	ItemError
)

type ToolExecution struct {
	CallID    string
	Name      string
	Args      string
	Result    string
	OK        bool
	Duration  int64
	Timestamp time.Time
}

type TranscriptItem struct {
	Type      TranscriptItemType
	Role      string // "user", "v100", "system"
	Text      string
	Images    [][]byte
	Tokens    []string   // accumulated token stream for ItemTokenGroup
	ToolExecs []*ToolExecution
	Expanded  bool
	ID        int
	Timestamp time.Time
}

type toggleTarget struct {
	lineNo int
	itemID int
}

type deviceStatus struct {
	CheckedAt      time.Time
	BatteryPresent bool
	Percent        int
	State          string
}

type SubmitRequest struct {
	Text   string
	Images [][]byte
}

// TUIModel is the Bubble Tea application model for the agent harness.
type TUIModel struct {
	width, height int

	transcript viewport.Model
	traceView  viewport.Model
	input      textinput.Model

	transcriptBuf strings.Builder
	traceBuf      strings.Builder
	lastTraceLine      string
	lastTraceCount     int
	lastTraceEventType core.EventType

	history        []*TranscriptItem
	nextItemID     int
	toggleTargets  []toggleTarget
	copyTargets    []copyTarget
	plainBuf       strings.Builder // plain-text transcript for full-copy
	inSubAgent     int             // nesting depth; >0 means inside agent.start..agent.end
	traceStepCount int             // running step count for trace pane
	activeAgents   []agentFrame
	agentDoneCount int
	agentFailCount int
	lastAgentNote  string
	device         deviceStatus
	modelEvents    []time.Time
	toolEvents     []time.Time
	compressEvents []time.Time
	lastEventAt    time.Time

	focus        focus
	showTrace    bool
	showStatus   bool
	pendConfirm  *confirmState
	statusMode   string
	statusLine   string
	statusTick   int
	downloadAnimTick int
	runSummary   string
	leftPanePct  int
	tracePanePct int
	verbose      bool
	showMetrics  bool

	// live metrics state
	currentStep   int
	maxSteps      int
	usedTokens    int
	maxTokens     int
	inputTokens   int
	outputTokens  int
	usedCost      float64
	maxCost       float64
	lastStepMS    int64
	lastStepTools int

	radioURL      string
	radioPlayer   string
	radioVolume   int
	radioPlaying  bool
	radioWave     string
	radioErr      string
	radioStep     int
	radioCmd      *exec.Cmd
	radioArtist   string
	radioTitle    string
	radioLastPoll time.Time

	showRadioSelect bool
	radioSelectIdx  int

	// clipboard images attached to current input
	pastedImages [][]byte

	// callbacks
	SubmitFn    func(SubmitRequest)
	InterruptFn func()
	onReady     func() // called once from Init() to signal event loop is active
}

// ── TUI styles ────────────────────────────────────────────────────────────────

var (
	tuiHeaderStyle = lipgloss.NewStyle().
			Foreground(clrPrimary).
			Bold(true)

	tuiHeaderDimStyle = lipgloss.NewStyle().
				Foreground(clrMuted)

	tuiPaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#374151"))

	tuiActivePaneStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrPrimary)

	tuiInputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#374151"))

	tuiInputActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(clrPrimary)

	tuiConfirmStyle = lipgloss.NewStyle().
			Bold(true).
			Border(lipgloss.DoubleBorder()).
			BorderForeground(clrDanger).
			Padding(1, 3)

	tuiTraceLabelStyle = lipgloss.NewStyle().
				Foreground(clrMuted).
				Italic(true)

	tuiStatusLabelStyle = lipgloss.NewStyle().
				Foreground(clrMuted).
				Italic(true)

	tuiCopyIconStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#374151"))
)

// EventMsg wraps a core.Event for the Bubble Tea message bus.
type EventMsg core.Event

// ConfirmMsg is sent when a confirmation result is available.
type ConfirmMsg struct {
	Approved bool
	confirm  *confirmState
}

// RequestConfirmMsg asks the TUI to show a confirmation dialog.
type RequestConfirmMsg struct {
	ToolName string
	Args     string
	Result   chan bool
}

type radioTickMsg struct{}
type deviceTickMsg struct{}
type radioNowPlayingMsg struct {
	Artist string
	Title  string
	Err    string
}
type downloadDoneMsg struct {
	artist string
	title  string
	err    string
}

// ImageRenderedMsg carries a rendered iTerm2 inline image string.
type ImageRenderedMsg struct {
	Image        string
	Index        int
	MessageIndex int // which user/model message this image belongs to
}

func (m *TUIModel) SetVerbose(v bool) {
	m.verbose = v
}

func (cs *confirmState) isActive() bool {
	return cs != nil && cs.active
}
