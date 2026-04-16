package ui

import (
	"fmt"
	"strings"

	"github.com/tripledoublev/v100/internal/core"
)

// appendTraceLine writes a rendered trace line to traceBuf, collapsing
// consecutive duplicate lines into a single line with a repeat count.
// Streaming events (tool.call_delta, model.token) are always collapsed since
// they produce many rapid updates and we'd rather show a single ×N line.
func (m *TUIModel) appendTraceLine(rendered string, evType core.EventType) {
	// Decide whether this event collapses into the previous trace line.
	collapse := false
	if m.lastTraceLine != "" {
		// Always collapse consecutive streaming deltas regardless of content.
		if evType == core.EventToolOutputDelta && m.lastTraceEventType == core.EventToolOutputDelta {
			collapse = true
		} else if evType == core.EventModelToken && m.lastTraceEventType == core.EventModelToken {
			collapse = true
		} else if rendered == m.lastTraceLine && evType == m.lastTraceEventType {
			collapse = true
		}
	}

	if collapse {
		m.lastTraceCount++
		// Remove the last line from traceBuf and rewrite with count.
		content := m.traceBuf.String()
		if idx := strings.LastIndex(content, "\n"); idx >= 0 {
			content = content[:idx]
		}
		m.traceBuf.Reset()
		m.traceBuf.WriteString(content)
		m.traceBuf.WriteString(m.lastTraceLine + styleMuted.Render(fmt.Sprintf(" ×%d", m.lastTraceCount+1)) + "\n")
	} else {
		// New unique line — reset collapse state.
		m.lastTraceLine = rendered
		m.lastTraceCount = 0
		m.lastTraceEventType = evType
		m.traceBuf.WriteString(rendered + "\n")
	}
}