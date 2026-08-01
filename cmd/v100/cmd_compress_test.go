package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func TestCheckpointHistoryIncludesTraceTail(t *testing.T) {
	runDir := t.TempDir()
	userPayload, err := json.Marshal(core.UserMsgPayload{Role: "user", Content: "before checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	ownerPayload, err := json.Marshal(core.ConversationMsgPayload{
		Role:            "assistant",
		Source:          "signal.owner_manual",
		ExternalEventID: "signal-owner-2",
		Content:         "Im in London",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := []core.Event{
		{Type: core.EventUserMsg, Payload: userPayload},
		{Type: core.EventConversationMsg, Payload: ownerPayload},
	}
	data, err := json.Marshal(CompressCheckpoint{
		Messages:        []providers.Message{{Role: "system", Content: "compressed earlier history"}},
		TraceEventCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "compress.checkpoint.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, ok, err := checkpointHistoryWithTraceTail(runDir, events)
	if err != nil || !ok {
		t.Fatalf("checkpoint history ok=%v err=%v", ok, err)
	}
	if len(msgs) != 2 || msgs[0].Content != "compressed earlier history" || msgs[1].Role != "assistant" || msgs[1].Content != "Im in London" {
		t.Fatalf("checkpoint plus tail = %#v", msgs)
	}
}
