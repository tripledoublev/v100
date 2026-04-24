package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/i18n"
)

// messageActionKind identifies which review action to perform on a message.
type messageActionKind int

const (
	actionCopy messageActionKind = iota
	actionAskCodex
	actionAskClaude
)

type messageActionTarget struct {
	lineNo, colStart, colEnd int
	action                   messageActionKind
	content                  string
	contextUser              string
}

// toolDetailTarget maps a rendered line to a specific ToolExecution.
type toolDetailTarget struct {
	lineNo    int
	exec      *ToolExecution
	groupID   int
	toolIndex int
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

// EventMsg wraps a core.Event for the Bubble Tea message bus.
type EventMsg core.Event

// ConfirmMsg is sent when a confirmation result is available.
type ConfirmMsg struct {
	Approved bool
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
	MessageIndex int
}


type ToolExecution struct {
	CallID    string // for matching call↔result events
	Name      string
	Args      string
	Result    string
	Success   bool
	Duration  time.Duration
	StartedAt time.Time
	DoneAt    time.Time
}

type agentFrame struct {
	RunID   string
	label   string
	depth   int
	Started time.Time
}

type ToolStatusMsg struct {
	Name       string
	Args       string
	Result     string
	Success    bool
	Duration   time.Duration
	StartedAt  time.Time
	FinishedAt time.Time
}

type RequestConfirmMsg struct {
	ToolName string
	Args     string
	Result   chan bool
}

type confirmState struct {
	toolName string
	args     string
	result   chan bool
}

type focus int

const (
	focusTranscript focus = iota
	focusInput
	focusTrace
	focusStatus
	focusDetail
)

type TranscriptItem struct {
	Type      TranscriptItemType
	Role      string // "user", "assistant", "system"
	Text      string
	Images    [][]byte
	Tokens    []string // accumulated token stream for ItemTokenGroup
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

type reviewDoneMsg struct {
	action messageActionKind
	itemID int
	output string
	err    error
}

// TUIModel is the Bubble Tea application model for the agent harness.
type TUIModel struct {
	width, height int

	transcript viewport.Model
	traceView  viewport.Model
	detailView viewport.Model
	input      textinput.Model

	transcriptBuf      strings.Builder
	traceBuf           strings.Builder
	lastTraceLine      string
	lastTraceCount     int
	lastTraceEventType core.EventType

	history        []*TranscriptItem
	nextItemID     int
	toggleTargets  []toggleTarget
	messageActions []messageActionTarget
	detailTargets  []toolDetailTarget // maps transcript line -> tool exec for click handling
	plainBuf       strings.Builder    // plain-text transcript for full-copy
	inSubAgent     int                // nesting depth; >0 means inside agent.start..agent.end
	traceStepCount int                // running step count for trace pane
	activeAgents   []agentFrame
	agentDoneCount int
	agentFailCount int
	lastAgentNote  string
	device         deviceStatus
	modelEvents    []time.Time
	toolEvents     []time.Time
	compressEvents []time.Time
	lastEventAt    time.Time

	focus            focus
	showTrace        bool
	showStatus       bool
	showDetail       bool
	selectedToolExec *ToolExecution
	pendConfirm      *confirmState
	statusMode       string
	StatusMode       i18n.StatusMode
	statusLine       string
	statusTick       int
	downloadAnimTick int
	runSummary       string
	leftPanePct      int
	tracePanePct     int
	detailPanePct    int
	codexAvailable   bool
	claudeAvailable  bool
	verbose          bool
	WorkspacePath    string
	showMetrics      bool

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
	reviewCancel    context.CancelFunc
	runReview       func(ctx context.Context, kind messageActionKind, prompt string) (string, error)

	// clipboard images attached to current input
	pastedImages [][]byte

	// image rendering subsystem
	imageRenderer *ImageRenderer

	// callbacks
	SubmitFn                    func(SubmitRequest)
	InterruptFn                 func()
	AppendConversationMessageFn func(role, content string)
	onReady                     func() // called once from Init() to signal event loop is active
}

func (m *TUIModel) SetVerbose(v bool) { m.verbose = v }

func (cs *confirmState) isActive() bool { return cs != nil }
