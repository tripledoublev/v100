package eval

import (
	"encoding/json"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
)

func TestSyncTraces_Identical(t *testing.T) {
	events := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
		{Type: core.EventRunEnd},
	}
	sd := SyncTraces("a", "b", events, events)
	if sd.DivergeIndex != -1 {
		t.Errorf("DivergeIndex = %d, want -1", sd.DivergeIndex)
	}
	if sd.DivergeType != "none" {
		t.Errorf("DivergeType = %q, want none", sd.DivergeType)
	}
	if len(sd.Segments) != 3 {
		t.Fatalf("Segments len = %d, want 3", len(sd.Segments))
	}
	for i, seg := range sd.Segments {
		if seg.Status != SegmentMatch {
			t.Errorf("segment %d status = %d, want SegmentMatch", i, seg.Status)
		}
		if seg.EventA == nil || seg.EventB == nil {
			t.Errorf("segment %d has nil event", i)
		}
	}
	if len(sd.CommonPrefix()) != 3 {
		t.Errorf("CommonPrefix len = %d, want 3", len(sd.CommonPrefix()))
	}
}

func TestSyncTraces_ToolChoiceDivergence(t *testing.T) {
	tcA, _ := json.Marshal(core.ToolCallPayload{Name: "fs_read"})
	tcB, _ := json.Marshal(core.ToolCallPayload{Name: "fs_list"})
	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall, Payload: tcA},
		{Type: core.EventToolResult},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall, Payload: tcB},
		{Type: core.EventToolResult},
	}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeIndex != 1 {
		t.Errorf("DivergeIndex = %d, want 1", sd.DivergeIndex)
	}
	if sd.DivergeType != "tool_choice" {
		t.Errorf("DivergeType = %q, want tool_choice", sd.DivergeType)
	}
	if len(sd.Segments) != 3 {
		t.Fatalf("Segments len = %d, want 3", len(sd.Segments))
	}
	if sd.Segments[0].Status != SegmentMatch {
		t.Error("segment 0 should be Match")
	}
	if sd.Segments[1].Status != SegmentDiverge {
		t.Error("segment 1 should be Diverge")
	}
	if sd.Segments[2].Status != SegmentMatch {
		t.Error("segment 2 should realign to Match")
	}
	if len(sd.CommonPrefix()) != 1 {
		t.Errorf("CommonPrefix len = %d, want 1", len(sd.CommonPrefix()))
	}
}

func TestSyncTraces_DifferentLengths(t *testing.T) {
	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
		{Type: core.EventToolCall},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
	}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeIndex != 1 {
		t.Errorf("DivergeIndex = %d, want 1", sd.DivergeIndex)
	}
	if sd.DivergeType != "length_mismatch" {
		t.Errorf("DivergeType = %q, want length_mismatch", sd.DivergeType)
	}
	if len(sd.Segments) != 3 {
		t.Fatalf("Segments len = %d, want 3", len(sd.Segments))
	}
	if sd.Segments[0].Status != SegmentMatch {
		t.Error("segment 0 should be Match")
	}
	if sd.Segments[1].Status != SegmentTailA {
		t.Errorf("segment 1 status = %d, want TailA", sd.Segments[1].Status)
	}
	if sd.Segments[1].EventA == nil {
		t.Error("segment 1 EventA should not be nil")
	}
	if sd.Segments[1].EventB != nil {
		t.Error("segment 1 EventB should be nil")
	}
}

func TestSyncTraces_BLonger(t *testing.T) {
	eventsA := []core.Event{{Type: core.EventRunStart}}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
	}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeType != "length_mismatch" {
		t.Errorf("DivergeType = %q, want length_mismatch", sd.DivergeType)
	}
	if len(sd.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(sd.Segments))
	}
	if sd.Segments[1].Status != SegmentTailB {
		t.Errorf("segment 1 status = %d, want TailB", sd.Segments[1].Status)
	}
}

func TestSyncTraces_Empty(t *testing.T) {
	sd := SyncTraces("a", "b", nil, nil)
	if sd.DivergeIndex != -1 {
		t.Errorf("DivergeIndex = %d, want -1", sd.DivergeIndex)
	}
	if len(sd.Segments) != 0 {
		t.Errorf("Segments len = %d, want 0", len(sd.Segments))
	}
}

func TestSyncTraces_DivergeAtStart(t *testing.T) {
	eventsA := []core.Event{{Type: core.EventModelCall}}
	eventsB := []core.Event{{Type: core.EventToolCall}}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeIndex != 0 {
		t.Errorf("DivergeIndex = %d, want 0", sd.DivergeIndex)
	}
	if sd.DivergeType != "event_type_mismatch" {
		t.Errorf("DivergeType = %q, want event_type_mismatch", sd.DivergeType)
	}
	if len(sd.CommonPrefix()) != 0 {
		t.Errorf("CommonPrefix len = %d, want 0", len(sd.CommonPrefix()))
	}
}

func TestSyncTraces_MidTraceInsertionRealignsLaterMatches(t *testing.T) {
	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
		{Type: core.EventToolCall},
		{Type: core.EventRunEnd},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall},
		{Type: core.EventRunEnd},
	}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeIndex != 1 {
		t.Fatalf("DivergeIndex = %d, want 1", sd.DivergeIndex)
	}
	if sd.DivergeType != "length_mismatch" {
		t.Fatalf("DivergeType = %q, want length_mismatch", sd.DivergeType)
	}
	if len(sd.Segments) != 4 {
		t.Fatalf("Segments len = %d, want 4", len(sd.Segments))
	}
	if sd.Segments[1].Status != SegmentTailA {
		t.Fatalf("segment 1 status = %d, want TailA", sd.Segments[1].Status)
	}
	if sd.Segments[2].Status != SegmentMatch {
		t.Fatalf("segment 2 status = %d, want Match after realignment", sd.Segments[2].Status)
	}
	if sd.Segments[3].Status != SegmentMatch {
		t.Fatalf("segment 3 status = %d, want Match after realignment", sd.Segments[3].Status)
	}
}

func TestSyncTraces_EventTypeMismatchCanRealignLaterMatch(t *testing.T) {
	eventsA := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventModelCall},
		{Type: core.EventRunEnd},
	}
	eventsB := []core.Event{
		{Type: core.EventRunStart},
		{Type: core.EventToolCall},
		{Type: core.EventRunEnd},
	}

	sd := SyncTraces("a", "b", eventsA, eventsB)
	if sd.DivergeType != "event_type_mismatch" {
		t.Fatalf("DivergeType = %q, want event_type_mismatch", sd.DivergeType)
	}
	if len(sd.Segments) != 3 {
		t.Fatalf("Segments len = %d, want 3", len(sd.Segments))
	}
	if sd.Segments[1].Status != SegmentDiverge {
		t.Fatalf("segment 1 status = %d, want Diverge", sd.Segments[1].Status)
	}
	if sd.Segments[2].Status != SegmentMatch {
		t.Fatalf("segment 2 status = %d, want Match after divergence", sd.Segments[2].Status)
	}
}
