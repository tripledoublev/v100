package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tripledoublev/v100/internal/core"
)

func (m *TUIModel) Init() tea.Cmd {
	if m.onReady != nil {
		m.onReady()
		m.onReady = nil
	}
	// Initial clear screen and status
	m.statusMode = "idle"
	m.statusLine = "initializing..."

	return tea.Batch(
		textinput.Blink,
		tea.WindowSize(),
		func() tea.Msg { return tea.ClearScreen() },
		// Read device status immediately on startup
		func() tea.Msg { return deviceTickMsg{} },
		radioTickCmd(),
		deviceTickCmd(),
		tea.EnableMouseCellMotion,
	)
}

func (m *TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		cmds = append(cmds, func() tea.Msg { return tea.ClearScreen() })

	case EventMsg:
		m.appendEvent(core.Event(msg))

	case RequestConfirmMsg:
		m.pendConfirm = &confirmState{
			active:   true,
			toolName: msg.ToolName,
			args:     msg.Args,
			approved: msg.Result,
		}

	case ConfirmMsg:
		if msg.confirm != nil {
			msg.confirm.approved <- msg.Approved
		}
		m.pendConfirm = nil

	case radioTickMsg:
		if cmd := m.onRadioTick(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, radioTickCmd())

	case deviceTickMsg:
		m.refreshDeviceStatus(time.Now())
		cmds = append(cmds, deviceTickCmd())

	case radioNowPlayingMsg:
		if msg.Err != "" {
			m.radioErr = msg.Err
		} else {
			m.radioArtist = strings.TrimSpace(msg.Artist)
			m.radioTitle = strings.TrimSpace(msg.Title)
		}

	case downloadDoneMsg:
		if msg.err != "" {
			m.radioErr = msg.err
			m.statusMode = "error"
			m.statusLine = "download failed"
		} else {
			m.radioErr = ""
			m.statusMode = "idle"
			m.statusLine = "downloaded: " + strings.TrimSpace(msg.artist+" - "+msg.title)
		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			m.handleMouseClick(msg.X, msg.Y)
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.stopRadio()
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace
			if !m.showTrace && (m.focus == focusTrace || m.focus == focusStatus) {
				m.focus = focusTranscript
				m.input.Blur()
			}

		case "ctrl+s":
			m.showStatus = !m.showStatus
			if !m.showStatus && m.focus == focusStatus {
				m.focus = focusTrace
			}
		case "ctrl+m":
			m.showMetrics = !m.showMetrics
		case "ctrl+a":
			if err := copyToClipboard(m.plainBuf.String()); err != nil {
				m.statusLine = "copy failed: " + err.Error()
				m.statusMode = "error"
			} else {
				m.statusLine = "full transcript copied to clipboard"
			}
		case "ctrl+v":
			if m.focus == focusInput {
				img, err := clipboardImageReader()
				if err != nil {
					m.statusLine = "paste failed: " + err.Error()
					m.statusMode = "error"
				} else {
					m.pastedImages = append(m.pastedImages, img)
					m.statusLine = fmt.Sprintf("attached %s", imageCount(len(m.pastedImages)))
					m.statusMode = "idle"
				}
				return m, nil
			}
		case "alt+r":
			m.showRadioSelect = !m.showRadioSelect
		case "ctrl+r":
			m.toggleRadio()
		case "]":
			m.adjustRadioVolume(5)
		case "[":
			m.adjustRadioVolume(-5)
		case "tab":
			m.cycleFocus()
		case "shift+tab":
			m.cycleFocusBack()
		case "ctrl+shift+tab", "ctrl+tab", "ctrl+pgup", "ctrl+pgdown":
			m.switchFocusHalf()
		case "ctrl+\\":
			m.switchFocusHalf()
		case "shift+left":
			m.resizeFocused(-4, 0)
		case "shift+right":
			m.resizeFocused(4, 0)
		case "shift+up":
			m.resizeFocused(0, 4)
		case "shift+down":
			m.resizeFocused(0, -4)

		case "ctrl+y":
			if m.pendConfirm.isActive() {
				confirm := m.pendConfirm
				return m, func() tea.Msg { return ConfirmMsg{Approved: true, confirm: confirm} }
			}

		case "ctrl+n":
			if m.pendConfirm.isActive() {
				confirm := m.pendConfirm
				return m, func() tea.Msg { return ConfirmMsg{Approved: false, confirm: confirm} }
			}

		case "up", "k":
			if m.showRadioSelect {
				m.radioSelectIdx--
				if m.radioSelectIdx < 0 {
					m.radioSelectIdx = len(availableStations) - 1
				}
				return m, nil
			}

		case "down", "j":
			if m.showRadioSelect {
				m.radioSelectIdx++
				if m.radioSelectIdx >= len(availableStations) {
					m.radioSelectIdx = 0
				}
				return m, nil
			}

		case "enter":
			if m.showRadioSelect {
				m.jumpToStation(m.radioSelectIdx)
				m.showRadioSelect = false
				return m, nil
			}
			if m.focus == focusInput && !m.pendConfirm.isActive() {
				val := sanitizeInputNoise(strings.TrimSpace(m.input.Value()))
				if val != "" || len(m.pastedImages) > 0 {
					images := append([][]byte(nil), m.pastedImages...)
					m.input.SetValue("")
					m.pastedImages = nil
					if cmd := m.handleBuiltInCommand(val); cmd != nil {
						cmds = append(cmds, cmd)
						break
					}
					if m.SubmitFn != nil {
						go m.SubmitFn(SubmitRequest{Text: val, Images: images})
					}
				}
			}

		case "esc":
			if m.showRadioSelect {
				m.showRadioSelect = false
				return m, nil
			}
			if m.InterruptFn != nil && (m.statusMode == "thinking" || m.statusMode == "tooling") {
				m.InterruptFn()
				m.focus = focusInput
				m.input.Focus()
				return m, nil
			}
		}
	}

	// Route key input to focused pane
	if _, ok := msg.(tea.KeyMsg); ok && !m.showRadioSelect {
		switch m.focus {
		case focusTranscript:
			var cmd tea.Cmd
			m.transcript, cmd = m.transcript.Update(msg)
			cmds = append(cmds, cmd)
		case focusTrace:
			var cmd tea.Cmd
			m.traceView, cmd = m.traceView.Update(msg)
			cmds = append(cmds, cmd)
		case focusInput:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		case focusStatus:
			// status pane is informational (no per-key updates needed)
		}
	}

	// Always sync viewports on non-key messages
	if _, ok := msg.(tea.KeyMsg); !ok {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(msg)
		cmds = append(cmds, cmd)
		m.traceView, cmd = m.traceView.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *TUIModel) cycleFocus() {
	if m.isInRightHalf() {
		if m.focus == focusTrace && m.showStatus {
			m.focus = focusStatus
			m.input.Blur()
			return
		}
		m.focus = focusTrace
		m.input.Blur()
		return
	}

	// Left half: transcript <-> input
	if m.focus == focusInput {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	m.focus = focusInput
	m.input.Focus()
}

func (m *TUIModel) cycleFocusBack() {
	if m.isInRightHalf() {
		if m.focus == focusStatus {
			m.focus = focusTrace
			m.input.Blur()
			return
		}
		if m.showStatus {
			m.focus = focusStatus
			m.input.Blur()
			return
		}
		m.focus = focusTrace
		m.input.Blur()
		return
	}

	// Left half: input <-> transcript
	if m.focus == focusInput {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	m.focus = focusInput
	m.input.Focus()
}

func (m *TUIModel) switchFocusHalf() {
	if m.isInRightHalf() {
		m.focus = focusTranscript
		m.input.Blur()
		return
	}
	if m.showTrace {
		m.focus = focusTrace
		m.input.Blur()
		return
	}
	m.focus = focusTranscript
	m.input.Blur()
}

func (m *TUIModel) isInRightHalf() bool {
	return m.focus == focusTrace || m.focus == focusStatus
}

func (m *TUIModel) resizeFocused(dxPct, dyPct int) {
	switch m.focus {
	case focusTranscript:
		m.leftPanePct = clampInt(m.leftPanePct+dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusTrace:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct+dyPct, 35, 85)
	case focusStatus:
		m.leftPanePct = clampInt(m.leftPanePct-dxPct, 45, 80)
		m.tracePanePct = clampInt(m.tracePanePct-dyPct, 35, 85)
	}
}

func (m *TUIModel) handleBuiltInCommand(input string) tea.Cmd {
	if strings.EqualFold(strings.TrimSpace(input), "download this song") {
		return m.startDownloadCmd()
	}
	if strings.EqualFold(strings.TrimSpace(input), "/radio") {
		m.showRadioSelect = true
		return nil
	}
	return nil
}
