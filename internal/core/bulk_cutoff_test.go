package core

import (
	"strings"
	"testing"

	"github.com/tripledoublev/v100/internal/providers"
)

func makeMsgs(sizes ...int) []providers.Message {
	out := make([]providers.Message, len(sizes))
	for i, sz := range sizes {
		out[i] = providers.Message{Role: "user", Content: strings.Repeat("x", sz)}
	}
	return out
}

func TestComputeBulkCutoff_TooShort(t *testing.T) {
	// 4 messages = exactly protected tail, no candidates
	msgs := makeMsgs(100, 100, 100, 100)
	if got := computeBulkCutoff(msgs); got != 0 {
		t.Errorf("expected 0 for protected-only history, got %d", got)
	}
}

func TestComputeBulkCutoff_LongHistory(t *testing.T) {
	// 10 messages: half = 5, which is fine
	msgs := makeMsgs(100, 100, 100, 100, 100, 100, 100, 100, 100, 100)
	if got := computeBulkCutoff(msgs); got != 5 {
		t.Errorf("expected 5 (half), got %d", got)
	}
}

func TestComputeBulkCutoff_HalfTooBig(t *testing.T) {
	// 8 messages: half = 4, but tail must be 4 protected → cutoff = 4 (n-protect = 4)
	msgs := makeMsgs(100, 100, 100, 100, 100, 100, 100, 100)
	got := computeBulkCutoff(msgs)
	if got != 4 {
		t.Errorf("expected 4 (clamped to n-protect), got %d", got)
	}
}

func TestComputeBulkCutoff_ShortHistory_NoSignificant(t *testing.T) {
	// 6 messages, small: half=3 (<4), no significant messages → return 0
	msgs := makeMsgs(100, 100, 100, 100, 100, 100)
	if got := computeBulkCutoff(msgs); got != 0 {
		t.Errorf("expected 0 for short uneventful history, got %d", got)
	}
}

func TestComputeBulkCutoff_ShortHistory_WithSignificant(t *testing.T) {
	// 6 messages, half=3, but msg[0] is significant → compress n-protect=2 oldest
	msgs := makeMsgs(8000, 100, 100, 100, 100, 100)
	got := computeBulkCutoff(msgs)
	if got != 2 {
		t.Errorf("expected 2 (n-protect) when significant message exists, got %d", got)
	}
}

func TestComputeBulkCutoff_ShortHistory_SignificantInTail(t *testing.T) {
	// Significant message is in the protected tail → don't compress
	msgs := makeMsgs(100, 100, 100, 8000, 100, 100)
	if got := computeBulkCutoff(msgs); got != 0 {
		t.Errorf("expected 0 when significant message is protected, got %d", got)
	}
}

func TestComputeBulkCutoff_Empty(t *testing.T) {
	if got := computeBulkCutoff(nil); got != 0 {
		t.Errorf("expected 0 for empty, got %d", got)
	}
}

func TestBulkSummaryMaxTokens_Sanity(t *testing.T) {
	if bulkSummaryMaxTokens < 200 || bulkSummaryMaxTokens > 2000 {
		t.Errorf("bulkSummaryMaxTokens out of reasonable range: %d", bulkSummaryMaxTokens)
	}
}
