# MEMORY — Tool Detail Pane (2025-04-18)

## Task: Tool Detail Pane Feature
- Status: COMPLETE ✓
- Branch: main (merged)

## What was implemented
A third column in the TUI that shows full tool call details when a tool is selected.

### State fields in TUIModel:
- `showDetail bool` — whether detail pane is visible
- `selectedToolExec *ToolExecution` — currently selected tool
- `detailView viewport.Model` — scrollable viewport for detail content
- `detailTargets []toolDetailTarget` — maps transcript lines to tool execs

### New files / changes:
- `internal/ui/panel.go`: DetailPanel with detailPaneContent() and formatDetailField()
- `internal/ui/input.go`: tryClickToolDetail() handler
- `internal/ui/view.go`: renderThreeColumnLayout() for 3-column layout
- `internal/ui/update.go`: Ctrl+D toggle, Esc dismiss, detail focus routing
- `internal/ui/layout.go`: "Ctrl+D:detail" hint in header
- `internal/ui/events.go`: populates detailTargets during rebuildTranscript()
- `internal/ui/types.go`: focusDetail enum, toolDetailTarget struct, showDetail field

### Key bindings:
- Click on tool result line → opens detail pane
- Ctrl+D → toggle detail pane
- Escape → dismiss detail pane

### Layout: Three columns when detail visible
- transcript (35%) | detail (35%) | trace+metrics+status (30%)
- Falls back to 2-column on narrow terminals

## Previous: Image Rendering (also complete)
- Internal cleanup from earlier session
