package signalstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStoreSurvivesReloadAndAcksAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "state.json")
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBinding("chat-a", "run-1", "session-1", now); err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplyOwnerEvent(OwnerEvent{ID: "owner-1", ChatID: "chat-a", SignalMessageID: "signal-1", Text: "context", CreatedAt: now})
	if err != nil || !applied {
		t.Fatalf("apply owner event: applied=%v err=%v", applied, err)
	}
	intent, added, err := store.EnqueueOutbound(OutboundIntent{ID: "out-1", ChatID: "chat-a", Text: "reply", CreatedAt: now})
	if err != nil || !added || intent.ContentHash == "" {
		t.Fatalf("enqueue outbound: intent=%+v added=%v err=%v", intent, added, err)
	}
	if _, err := store.MarkProcessed("signal-1", now); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := reloaded.Binding("chat-a")
	if !ok || binding.RunID != "run-1" || binding.SessionID != "session-1" {
		t.Fatalf("binding lost across reload: %+v, %v", binding, ok)
	}
	if !reloaded.IsProcessed("signal-1") {
		t.Fatal("processed ID lost across reload")
	}
	if got := reloaded.PendingOwnerEvents(); len(got) != 1 || got[0].ID != "owner-1" {
		t.Fatalf("pending owner events = %+v", got)
	}
	if got := reloaded.PendingOutbound(); len(got) != 1 || got[0].ID != "out-1" {
		t.Fatalf("pending outbound = %+v", got)
	}

	if err := reloaded.AckOwnerEvent("owner-1"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.AckOwnerEvent("owner-1"); err != nil {
		t.Fatalf("second owner ack must be a no-op: %v", err)
	}
	if err := reloaded.AckOutbound("out-1"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.AckOutbound("out-1"); err != nil {
		t.Fatalf("second outbound ack must be a no-op: %v", err)
	}

	afterAck, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterAck.PendingOwnerEvents()) != 0 || len(afterAck.PendingOutbound()) != 0 {
		t.Fatal("acknowledged payloads reappeared after reload")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "context") || strings.Contains(string(data), "reply") {
		t.Fatalf("acknowledged transcript content retained: %s", data)
	}
}

func TestApplyOperationsAreIdempotent(t *testing.T) {
	store, err := OpenDefault(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := OwnerEvent{ChatID: "chat", SignalMessageID: "same", Timestamp: 42, Text: "hello", CreatedAt: now}
	first, err := store.ApplyOwnerEvent(event)
	if err != nil || !first {
		t.Fatalf("first apply: %v, %v", first, err)
	}
	retry := event
	retry.Text = "hello after normalization"
	second, err := store.ApplyOwnerEvent(retry)
	if err != nil || second {
		t.Fatalf("duplicate apply: %v, %v", second, err)
	}
	if len(store.PendingOwnerEvents()) != 1 {
		t.Fatal("duplicate owner event was stored")
	}

	if added, err := store.MarkProcessed("message", now); err != nil || !added {
		t.Fatalf("first processed apply: %v, %v", added, err)
	}
	if added, err := store.MarkProcessed("message", now.Add(time.Second)); err != nil || added {
		t.Fatalf("duplicate processed apply: %v, %v", added, err)
	}

	firstIntent, added, err := store.EnqueueOutbound(OutboundIntent{ID: "stable", ChatID: "chat", Text: "one", CreatedAt: now})
	if err != nil || !added {
		t.Fatalf("first enqueue: %v, %v", added, err)
	}
	secondIntent, added, err := store.EnqueueOutbound(OutboundIntent{ID: "stable", ChatID: "chat", Text: "changed", CreatedAt: now})
	if err != nil || added || secondIntent.Text != firstIntent.Text {
		t.Fatalf("duplicate enqueue changed intent: %+v, %v, %v", secondIntent, added, err)
	}
}

func TestClearChatIsIdempotentAndPreservesOtherState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for _, chatID := range []string{"clear", "keep"} {
		if err := store.SetBinding(chatID, "run-"+chatID, "session-"+chatID, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyOwnerEvent(OwnerEvent{ID: "owner-" + chatID, ChatID: chatID, Text: chatID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.EnqueueOutbound(OutboundIntent{ID: "out-" + chatID, ChatID: chatID, Text: chatID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.MarkProcessed("processed-for-cleared-chat", now); err != nil {
		t.Fatal(err)
	}

	if err := store.ClearChat("clear"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearChat("clear"); err != nil {
		t.Fatalf("repeated clear must be a no-op: %v", err)
	}

	reloaded, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Binding("clear"); ok {
		t.Fatal("cleared chat binding remains")
	}
	if binding, ok := reloaded.Binding("keep"); !ok || binding.RunID != "run-keep" {
		t.Fatalf("other chat binding was changed: %+v, %v", binding, ok)
	}
	if got := reloaded.PendingOwnerEvents(); len(got) != 1 || got[0].ID != "owner-keep" {
		t.Fatalf("pending owner events after clear = %+v", got)
	}
	if got := reloaded.PendingOutbound(); len(got) != 1 || got[0].ID != "out-keep" {
		t.Fatalf("pending outbound after clear = %+v", got)
	}
	if !reloaded.IsProcessed("processed-for-cleared-chat") {
		t.Fatal("ClearChat removed global processed Signal IDs")
	}
}

func TestMatchEchoTimestampFirstThenIdenticalTextFIFO(t *testing.T) {
	limits := DefaultLimits()
	limits.EchoTTL = 5 * time.Minute
	store, err := Open(filepath.Join(t.TempDir(), "state.json"), limits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"first", "second", "third"} {
		_, _, err := store.EnqueueOutbound(OutboundIntent{ID: id, ChatID: "chat", Text: "identical", CreatedAt: now.Add(time.Duration(i) * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkOutboundPossiblySent("first"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOutboundPossiblySent("third"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOutboundSent("second", 222); err != nil {
		t.Fatal(err)
	}
	matched, ok, err := store.MatchEcho("chat", "identical", 222, now.Add(time.Minute))
	if err != nil || !ok || matched.ID != "second" {
		t.Fatalf("timestamp match = %+v, %v, %v", matched, ok, err)
	}
	matched, ok, err = store.MatchEcho("chat", "identical", 0, now.Add(2*time.Minute))
	if err != nil || !ok || matched.ID != "first" {
		t.Fatalf("first fallback match = %+v, %v, %v", matched, ok, err)
	}
	matched, ok, err = store.MatchEcho("chat", "identical", 0, now.Add(2*time.Minute))
	if err != nil || !ok || matched.ID != "third" {
		t.Fatalf("second fallback match = %+v, %v, %v", matched, ok, err)
	}
	_, _, err = store.EnqueueOutbound(OutboundIntent{ID: "expired", ChatID: "chat", Text: "old", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.MatchEcho("chat", "old", 0, now.Add(6*time.Minute)); err != nil || ok {
		t.Fatalf("expired fallback matched: %v, %v", ok, err)
	}
	if _, _, err := store.EnqueueOutbound(OutboundIntent{ID: "never-sent", ChatID: "chat", Text: "manual owner text", CreatedAt: now.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.MatchEcho("chat", "manual owner text", 0, now.Add(4*time.Minute)); err != nil || ok {
		t.Fatalf("unconfirmed intent swallowed owner message: %v, %v", ok, err)
	}
}

func TestCorruptStateIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	quarantined := store.QuarantinedPath()
	if quarantined == "" || !strings.HasPrefix(quarantined, path+".corrupt.") {
		t.Fatalf("unexpected quarantine path %q", quarantined)
	}
	data, err := os.ReadFile(quarantined)
	if err != nil || string(data) != "{not-json" {
		t.Fatalf("quarantined data = %q, %v", data, err)
	}
	var disk map[string]any
	data, err = os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &disk) != nil || disk["version"] != float64(CurrentVersion) {
		t.Fatalf("replacement state invalid: %s, %v", data, err)
	}
}

func TestUnsupportedVersionIsNotQuarantined(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{"version":999,"chats":{}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenDefault(path)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != string(data) {
		t.Fatalf("newer state was modified: %q, %v", got, readErr)
	}
}

func TestStatePermissionsAreRestricted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "state-dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"chats":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkProcessed("id", time.Now()); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %o", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %o", got)
	}
}

func TestOpenIgnoresOrphanedAtomicTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkProcessed("committed", time.Now()); err != nil {
		t.Fatal(err)
	}
	// A process dying before rename can leave a partial temporary file. It must
	// not affect the last atomically committed state.
	if err := os.WriteFile(filepath.Join(dir, ".signal-state-crashed"), []byte(`{"version":`), 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := OpenDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsProcessed("committed") || reloaded.QuarantinedPath() != "" {
		t.Fatal("orphaned temp file affected committed state")
	}
}

func TestMetadataIsBounded(t *testing.T) {
	limits := Limits{Chats: 2, OwnerEvents: 2, ProcessedIDs: 2, OutboundIntents: 2, EchoTTL: time.Minute}
	store, err := Open(filepath.Join(t.TempDir(), "state.json"), limits)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		now := base.Add(time.Duration(i) * time.Second)
		if err := store.SetBinding(id, "run-"+id, "session-"+id, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.MarkProcessed(id, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyOwnerEvent(OwnerEvent{ID: id, ChatID: id, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.EnqueueOutbound(OutboundIntent{ID: id, ChatID: id, Text: id, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := store.Binding("a"); ok {
		t.Fatal("oldest chat binding was not evicted")
	}
	if store.IsProcessed("a") || !store.IsProcessed("c") {
		t.Fatal("processed ID bound was not enforced")
	}
	if got := store.PendingOwnerEvents(); len(got) != 2 || got[0].ID != "b" {
		t.Fatalf("bounded owner events = %+v", got)
	}
	if got := store.PendingOutbound(); len(got) != 2 || got[0].ID != "b" {
		t.Fatalf("bounded outbound intents = %+v", got)
	}
}
