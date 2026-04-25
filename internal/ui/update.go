package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/i18n"
)

func (m *TUIModel) Init() tea.Cmd {
	if m.onReady != nil {
		m.onReady()
		m.onReady = nil
	}
	m.StatusMode = i18n.StatusIdle
	m.statusMode = m.StatusMode.String()
	m.statusLine = i18n.T("status_ready")
	return tea.Batch(
		textinput.Blink,
		tea.WindowSize(),
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
			toolName: msg.ToolName,
			args:     msg.Args,
			result:   msg.Result,
		}

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
			m.StatusMode = i18n.StatusError
			m.statusMode = m.StatusMode.String()
			m.statusLine = "download failed"
		} else {
			m.radioErr = ""
			m.StatusMode = i18n.StatusIdle
			m.statusMode = m.StatusMode.String()
			m.statusLine = "downloaded: " + strings.TrimSpace(msg.artist+" - "+msg.title)
		}

	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if cmd := m.handleMouseClick(msg.X, msg.Y); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case reviewDoneMsg:
		if m.reviewCancel != nil {
			m.reviewCancel = nil
		}
		for _, item := range m.history {
			if item.ID == msg.itemID {
				item.Text = msg.output
				if strings.TrimSpace(item.Text) == "" && msg.err != nil {
					item.Text = truncateReviewStatus(msg.err.Error())
				}
				break
			}
		}
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.statusMode = "idle"
				m.statusLine = reviewLabel(msg.action) + " canceled"
				m.rebuildTranscript(true)
				break
			}
			m.statusMode = "error"
			m.statusLine = reviewLabel(msg.action) + " failed: " + truncateReviewStatus(msg.output)
			if strings.TrimSpace(msg.output) == "" {
				m.statusLine = reviewLabel(msg.action) + " failed: " + truncateReviewStatus(msg.err.Error())
			}
		} else {
			m.statusMode = "idle"
			m.statusLine = reviewLabel(msg.action) + " replied"
			if strings.TrimSpace(msg.output) != "" && m.AppendConversationMessageFn != nil {
				label := reviewLabel(msg.action)
				content := "[external review: " + label + "]\n" +
					"This is feedback on the previous assistant response. Incorporate it when relevant.\n\n" +
					msg.output
				m.AppendConversationMessageFn("system", content)
			}
		}
		m.rebuildTranscript(true)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.stopRadio()
			return m, tea.Quit

		case "ctrl+t":
			m.showTrace = !m.showTrace
			if !m.showTrace && (m.focus == focusTrace || m.focus == focusStatus) {
				m.activateFocus(focusTranscript)
			}

		case "ctrl+d":
			if m.selectedToolExec != nil {
				m.showDetail = !m.showDetail
				if m.showDetail {
					m.activateFocus(focusDetail)
				} else if m.focus == focusDetail {
					m.activateFocus(focusTranscript)
				}
			}
			return m, nil

		case "ctrl+s":
			m.showStatus = !m.showStatus
			if !m.showStatus && m.focus == focusStatus {
				m.activateFocus(focusTrace)
			}

		case "ctrl+m":
			m.showMetrics = !m.showMetrics
			return m, nil

		case "ctrl+a":
			if err := clipboardCopyWriter(m.plainBuf.String()); err != nil {
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
			if m.pendConfirm != nil {
				m.pendConfirm.result <- true
				m.pendConfirm = nil
			}

		case "ctrl+n":
			if m.pendConfirm != nil {
				m.pendConfirm.result <- false
				m.pendConfirm = nil
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
			if m.reviewCancel != nil {
				m.reviewCancel()
				m.reviewCancel = nil
				m.statusMode = "idle"
				m.statusLine = "review canceled"
				return m, nil
			}
			if m.showRadioSelect {
				m.showRadioSelect = false
				return m, nil
			}
			if m.showDetail && m.focus == focusDetail {
				m.showDetail = false
				m.activateFocus(focusTranscript)
				return m, nil
			}
			if m.InterruptFn != nil && (m.statusMode == "thinking" || m.statusMode == "tooling") {
				m.InterruptFn()
				m.activateFocus(focusInput)
				return m, nil
			}
			if m.imageRenderer != nil {
				m.imageRenderer.ClearAll()
				m.statusLine = "cleared inline images"
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
		case focusDetail:
			var cmd tea.Cmd
			m.detailView, cmd = m.detailView.Update(msg)
			cmds = append(cmds, cmd)
		case focusInput:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Sync viewports on non-key messages
	if _, ok := msg.(tea.KeyMsg); !ok {
		var cmd tea.Cmd
		m.transcript, cmd = m.transcript.Update(msg)
		cmds = append(cmds, cmd)
		m.traceView, cmd = m.traceView.Update(msg)
		cmds = append(cmds, cmd)
		if m.showDetail {
			m.detailView, cmd = m.detailView.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}
