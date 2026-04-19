package ui

import (
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
)

func (m *TUIModel) precedingUserMessage(itemIndex int) string {
	for i := itemIndex - 1; i >= 0; i-- {
		item := m.history[i]
		if item.Type == ItemMessage && item.Role == "user" {
			return item.Text
		}
	}
	return ""
}

func (m *TUIModel) writeMessageActionRow(indent string, item *TranscriptItem, itemIndex int) {
	if item.Type != ItemMessage {
		return
	}

	type button struct {
		label  string
		action messageActionKind
	}

	buttons := []button{{label: "[⎘ copy]", action: actionCopy}}
	if item.Role == "assistant" && m.codexAvailable {
		buttons = append(buttons, button{label: "[ask codex]", action: actionAskCodex})
	}
	if item.Role == "assistant" && m.claudeAvailable {
		buttons = append(buttons, button{label: "[ask claude]", action: actionAskClaude})
	}

	contextUser := m.precedingUserMessage(itemIndex)
	lineNo := strings.Count(m.transcriptBuf.String(), "\n")
	m.transcriptBuf.WriteString(indent)
	col := lipgloss.Width(indent)
	for i, btn := range buttons {
		if i > 0 {
			m.transcriptBuf.WriteByte(' ')
			col++
		}
		start := col
		col += lipgloss.Width(btn.label)
		m.transcriptBuf.WriteString(tuiCopyIconStyle.Render(btn.label))
		m.messageActions = append(m.messageActions, messageActionTarget{
			lineNo:      lineNo,
			colStart:    start,
			colEnd:      col,
			action:      btn.action,
			content:     item.Text,
			contextUser: contextUser,
		})
	}
	m.transcriptBuf.WriteByte('\n')
}
