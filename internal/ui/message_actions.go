package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

func buildReviewPrompt(contextUser, assistantText string) string {
	if strings.TrimSpace(contextUser) == "" {
		return "Please review the following assistant response.\n\nAssistant response:\n" + assistantText
	}
	return "Please review the following assistant response.\n\nOriginal user request:\n" + contextUser + "\n\nAssistant response:\n" + assistantText
}

func realRunReview(ctx context.Context, kind messageActionKind, prompt string) (string, error) {
	var cmd *exec.Cmd
	switch kind {
	case actionAskCodex:
		cmd = exec.CommandContext(ctx, "codex", "exec", prompt)
	case actionAskClaude:
		cmd = exec.CommandContext(ctx, "claude", "-p", prompt)
	default:
		return "", nil
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func reviewLabel(kind messageActionKind) string {
	switch kind {
	case actionAskCodex:
		return "codex"
	case actionAskClaude:
		return "claude"
	default:
		return "assistant"
	}
}

func truncateReviewStatus(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func (m *TUIModel) startReview(action messageActionKind, target messageActionTarget) tea.Cmd {
	if m.reviewCancel != nil || m.runReview == nil {
		return nil
	}

	item := &TranscriptItem{
		Type:      ItemMessage,
		Role:      reviewLabel(action),
		Text:      "",
		Timestamp: time.Now(),
	}
	m.addItem(item)
	m.rebuildTranscript(true)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	m.reviewCancel = cancel
	m.statusMode = "thinking"
	m.statusLine = "asking " + reviewLabel(action) + "..."

	prompt := buildReviewPrompt(target.contextUser, target.content)
	return func() tea.Msg {
		output, err := m.runReview(ctx, action, prompt)
		return reviewDoneMsg{action: action, itemID: item.ID, output: output, err: err}
	}
}
