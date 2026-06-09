package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/i18n"
)

func TestAssistantActionRowRendersEligibleButtons(t *testing.T) {
	m := NewTUIModel(true, true)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "user", Text: "fix it", Timestamp: time.Now()},
		{ID: 2, Type: ItemMessage, Role: "assistant", Text: "patched", Timestamp: time.Now()},
	}

	m.rebuildTranscript(true)
	out := stripANSI(m.transcriptBuf.String())
	if !strings.Contains(out, "[⎘ copy] [ask codex] [ask claude]") {
		t.Fatalf("expected assistant action row, got %q", out)
	}
}

func TestUserAndSystemMessagesRenderCopyOnlyRow(t *testing.T) {
	m := NewTUIModel(true, true)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "user", Text: "hello", Timestamp: time.Now()},
		{ID: 2, Type: ItemMessage, Role: "system", Text: "notice", Timestamp: time.Now()},
	}

	m.rebuildTranscript(true)
	out := stripANSI(m.transcriptBuf.String())
	if strings.Contains(out, "[ask codex]") || strings.Contains(out, "[ask claude]") {
		t.Fatalf("did not expect review buttons for user/system messages, got %q", out)
	}
	if strings.Count(out, "[⎘ copy]") != 2 {
		t.Fatalf("expected copy rows for user/system messages, got %q", out)
	}
}

func TestActionRowVisibilityFollowsAvailableCLIs(t *testing.T) {
	m := NewTUIModel(true, false)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "assistant", Text: "done", Timestamp: time.Now()},
	}

	m.rebuildTranscript(true)
	out := stripANSI(m.transcriptBuf.String())
	if !strings.Contains(out, "[ask codex]") {
		t.Fatalf("expected codex button, got %q", out)
	}
	if strings.Contains(out, "[ask claude]") {
		t.Fatalf("did not expect claude button, got %q", out)
	}
}

func TestActionHitboxesAreNonOverlappingAndWhitespaceIsNotClickable(t *testing.T) {
	m := NewTUIModel(true, true)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "assistant", Text: "done", Timestamp: time.Now()},
	}
	m.rebuildTranscript(true)

	if len(m.messageActions) != 3 {
		t.Fatalf("messageActions = %d, want 3", len(m.messageActions))
	}
	for i := 1; i < len(m.messageActions); i++ {
		prev := m.messageActions[i-1]
		cur := m.messageActions[i]
		if cur.lineNo != prev.lineNo {
			t.Fatalf("lineNo mismatch: %d vs %d", prev.lineNo, cur.lineNo)
		}
		if cur.colStart <= prev.colEnd {
			t.Fatalf("overlapping hitboxes: prev=%+v cur=%+v", prev, cur)
		}
	}

	spaceTermX := m.messageActions[0].colEnd + 1
	if cmd := m.tryClickMessageAction(spaceTermX, m.messageActions[0].lineNo+2); cmd != nil {
		t.Fatal("expected whitespace between buttons to be non-clickable")
	}
}

func TestActionClickBoundaries(t *testing.T) {
	m := NewTUIModel(true, false)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "assistant", Text: "done", Timestamp: time.Now()},
	}
	m.rebuildTranscript(true)

	target := m.messageActions[0]
	copyCalled := 0
	prevCopy := clipboardCopyWriter
	clipboardCopyWriter = func(text string) error {
		copyCalled++
		if text != "done" {
			t.Fatalf("copied text = %q, want done", text)
		}
		return nil
	}
	defer func() { clipboardCopyWriter = prevCopy }()

	if cmd := m.tryClickMessageAction(target.colStart+1, target.lineNo+2); cmd != nil {
		t.Fatal("copy action should not return a command")
	}
	if copyCalled != 1 {
		t.Fatalf("copyCalled = %d, want 1", copyCalled)
	}

	if cmd := m.tryClickMessageAction(target.colEnd, target.lineNo+2); cmd != nil {
		t.Fatal("click at colEnd should miss")
	}
}

func TestActionHitboxesRebuildAcrossWidths(t *testing.T) {
	m := NewTUIModel(true, true)
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "assistant", Text: "done", Timestamp: time.Now()},
	}
	m.width = 80
	m.height = 30
	m.rebuildTranscript(true)
	first := append([]messageActionTarget(nil), m.messageActions...)

	m.width = 120
	m.rebuildTranscript(true)
	second := append([]messageActionTarget(nil), m.messageActions...)

	if len(first) != len(second) {
		t.Fatalf("action count changed across rebuild: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].colStart != second[i].colStart || first[i].colEnd != second[i].colEnd {
			t.Fatalf("expected stable hitboxes across widths for fixed row, got %+v vs %+v", first[i], second[i])
		}
	}
}

func TestBuildReviewPrompt(t *testing.T) {
	withContext := buildReviewPrompt("fix the bug", "here is the patch")
	wantWith := "Please review the following assistant response.\n\nOriginal user request:\nfix the bug\n\nAssistant response:\nhere is the patch"
	if withContext != wantWith {
		t.Fatalf("with context = %q, want %q", withContext, wantWith)
	}

	withoutContext := buildReviewPrompt("", "here is the patch")
	wantWithout := "Please review the following assistant response.\n\nAssistant response:\nhere is the patch"
	if withoutContext != wantWithout {
		t.Fatalf("without context = %q, want %q", withoutContext, wantWithout)
	}
}

func TestPrecedingUserMessageReturnsNearestPriorUser(t *testing.T) {
	m := NewTUIModel(false, false)
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "user", Text: "first", Timestamp: time.Now()},
		{ID: 2, Type: ItemMessage, Role: "assistant", Text: "reply", Timestamp: time.Now()},
		{ID: 3, Type: ItemMessage, Role: "user", Text: "second", Timestamp: time.Now()},
		{ID: 4, Type: ItemMessage, Role: "assistant", Text: "answer", Timestamp: time.Now()},
	}

	if got := m.precedingUserMessage(3); got != "second" {
		t.Fatalf("precedingUserMessage = %q, want second", got)
	}
	if got := m.precedingUserMessage(1); got != "first" {
		t.Fatalf("precedingUserMessage = %q, want first", got)
	}
	if got := m.precedingUserMessage(0); got != "" {
		t.Fatalf("precedingUserMessage = %q, want empty", got)
	}
}

func TestAskActionAppendsReplyAndUsesStubbedRunner(t *testing.T) {
	m := NewTUIModel(true, false)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "user", Text: "fix the bug", Timestamp: time.Now()},
		{ID: 2, Type: ItemMessage, Role: "assistant", Text: "patched it", Timestamp: time.Now()},
	}
	m.nextItemID = 3
	m.rebuildTranscript(true)

	var gotPrompt string
	m.runReview = func(ctx context.Context, kind messageActionKind, prompt string) (string, error) {
		gotPrompt = prompt
		if kind != actionAskCodex {
			t.Fatalf("kind = %v, want actionAskCodex", kind)
		}
		return "second opinion", nil
	}

	var cmd tea.Cmd
	for _, action := range m.messageActions {
		if action.action == actionAskCodex {
			cmd = m.tryClickMessageAction(action.colStart+1, action.lineNo+2)
			break
		}
	}
	if cmd == nil {
		t.Fatal("expected ask codex to return a command")
	}

	msg := cmd()
	updated, _ := m.Update(msg)
	m = updated.(*TUIModel)

	if gotPrompt == "" || !strings.Contains(gotPrompt, "fix the bug") || !strings.Contains(gotPrompt, "patched it") {
		t.Fatalf("prompt = %q, want both user and assistant context", gotPrompt)
	}
	last := m.history[len(m.history)-1]
	if last.Role != "codex" || last.Text != "second opinion" {
		t.Fatalf("last item = %+v, want codex reply", last)
	}
	if m.statusLine != "codex replied" {
		t.Fatalf("statusLine = %q, want codex replied", m.statusLine)
	}
}

func TestReviewDoneRendersTranscriptBeforePersistingReview(t *testing.T) {
	m := NewTUIModel(false, false)
	m.width = 100
	m.height = 30
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "codex", Text: "", Timestamp: time.Now()},
	}
	m.nextItemID = 2
	m.rebuildTranscript(true)

	var called bool
	var gotRole, gotContent string
	m.AppendConversationMessageFn = func(role, content string) {
		called = true
		gotRole = role
		gotContent = content
	}

	updated, cmd := m.Update(reviewDoneMsg{
		action: actionAskCodex,
		itemID: 1,
		output: "second opinion",
	})
	m = updated.(*TUIModel)

	if called {
		t.Fatal("persistence callback ran synchronously before Update returned")
	}
	if cmd == nil {
		t.Fatal("expected review persistence command")
	}
	if out := stripANSI(m.transcriptBuf.String()); !strings.Contains(out, "second opinion") {
		t.Fatalf("transcript buffer missing review output: %q", out)
	}
	if view := stripANSI(m.transcript.View()); !strings.Contains(view, "second opinion") {
		t.Fatalf("visible transcript missing review output: %q", view)
	}
	if plain := m.plainBuf.String(); !strings.Contains(plain, "codex: second opinion") {
		t.Fatalf("plain transcript missing review output: %q", plain)
	}

	if msg := cmd(); msg != nil {
		t.Fatalf("persistence command message = %T, want nil", msg)
	}
	if !called {
		t.Fatal("persistence callback did not run from returned command")
	}
	if gotRole != "system" {
		t.Fatalf("persisted role = %q, want system", gotRole)
	}
	if !strings.Contains(gotContent, "[external review: codex]") || !strings.Contains(gotContent, "second opinion") {
		t.Fatalf("persisted content = %q", gotContent)
	}

	m.appendEvent(reviewerEvent(t, "codex", "second opinion"))
	if plain := m.plainBuf.String(); strings.Count(plain, "codex: second opinion") != 1 {
		t.Fatalf("plain transcript duplicated review output after trace echo: %q", plain)
	}
}

func TestReviewerTraceEventRendersAsReviewRoleOnResume(t *testing.T) {
	m := NewTUIModel(false, false)

	m.appendEvent(reviewerEvent(t, "claude", "second opinion"))

	last := m.history[len(m.history)-1]
	if last.Role != "claude" || last.Text != "second opinion" {
		t.Fatalf("last item = %+v, want claude review", last)
	}
	out := stripANSI(m.transcriptBuf.String())
	if !strings.Contains(out, "claude") || !strings.Contains(out, "second opinion") {
		t.Fatalf("transcript missing reviewer content: %q", out)
	}
	if strings.Contains(out, "[external review:") {
		t.Fatalf("transcript leaked persistence wrapper: %q", out)
	}
}

func TestReviewerTraceEventDoesNotDuplicateVisibleReviewItem(t *testing.T) {
	m := NewTUIModel(false, false)
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "codex", Text: "second opinion", Timestamp: time.Now(), pendingReviewTrace: true},
	}
	m.nextItemID = 2
	m.rebuildTranscript(true)

	m.appendEvent(reviewerEvent(t, "codex", "second opinion"))

	var matches int
	for _, item := range m.history {
		if item.Type == ItemMessage && item.Role == "codex" && item.Text == "second opinion" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("codex review message count = %d, want 1", matches)
	}
	out := stripANSI(m.transcriptBuf.String())
	if strings.Count(out, "second opinion") != 1 {
		t.Fatalf("transcript duplicated review output: %q", out)
	}
	if m.history[0].pendingReviewTrace {
		t.Fatal("expected pending review trace marker to be consumed")
	}
}

func TestReviewerTraceEventsKeepDistinctSameTextReviews(t *testing.T) {
	m := NewTUIModel(false, false)

	m.appendEvent(reviewerEvent(t, "codex", "same note"))
	m.appendEvent(reviewerEvent(t, "codex", "same note"))

	var matches int
	for _, item := range m.history {
		if item.Type == ItemMessage && item.Role == "codex" && item.Text == "same note" {
			matches++
		}
	}
	if matches != 2 {
		t.Fatalf("codex same-text review message count = %d, want 2", matches)
	}
}

func TestReviewerTraceEventDoesNotOverrideReplyStatus(t *testing.T) {
	m := NewTUIModel(false, false)
	m.statusMode = "idle"
	m.StatusMode = i18n.StatusIdle
	m.statusLine = "codex replied"

	m.appendEvent(reviewerEvent(t, "codex", "second opinion"))

	if m.statusLine != "codex replied" {
		t.Fatalf("statusLine = %q, want codex replied", m.statusLine)
	}
	if m.statusMode != "idle" {
		t.Fatalf("statusMode = %q, want idle", m.statusMode)
	}
}

func reviewerEvent(t *testing.T, label, output string) core.Event {
	t.Helper()
	payload, err := json.Marshal(core.UserMsgPayload{
		Source:  "reviewer",
		Content: externalReviewConversationContent(label, output),
	})
	if err != nil {
		t.Fatal(err)
	}
	return core.Event{
		RunID:   "run-test",
		TS:      time.Now(),
		Type:    core.EventUserMsg,
		Payload: payload,
	}
}

func TestEscCancelsReview(t *testing.T) {
	m := NewTUIModel(true, false)
	canceled := false
	m.reviewCancel = func() { canceled = true }

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(*TUIModel)

	if !canceled {
		t.Fatal("expected cancel func to run")
	}
	if m.statusLine != "review canceled" {
		t.Fatalf("statusLine = %q, want review canceled", m.statusLine)
	}
}

func TestReviewDoneCanceledSetsCanceledStatus(t *testing.T) {
	m := NewTUIModel(false, false)
	m.reviewCancel = func() {}
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "codex", Text: "", Timestamp: time.Now()},
	}

	updated, _ := m.Update(reviewDoneMsg{
		action: actionAskCodex,
		itemID: 1,
		err:    context.Canceled,
	})
	m = updated.(*TUIModel)
	if m.statusLine != "codex canceled" {
		t.Fatalf("statusLine = %q, want codex canceled", m.statusLine)
	}
}

func TestReviewFailureUsesOutputSnippet(t *testing.T) {
	m := NewTUIModel(false, false)
	m.reviewCancel = func() {}
	m.history = []*TranscriptItem{
		{ID: 1, Type: ItemMessage, Role: "claude", Text: "", Timestamp: time.Now()},
	}

	updated, _ := m.Update(reviewDoneMsg{
		action: actionAskClaude,
		itemID: 1,
		output: "some stderr output",
		err:    errors.New("exit status 1"),
	})
	m = updated.(*TUIModel)
	if !strings.Contains(m.statusLine, "claude failed: some stderr output") {
		t.Fatalf("statusLine = %q", m.statusLine)
	}
}
