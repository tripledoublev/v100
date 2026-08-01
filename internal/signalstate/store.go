// Package signalstate persists the small amount of coordination state needed by
// a restart-tolerant Signal gateway. It intentionally does not store completed
// conversation history.
package signalstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const CurrentVersion = 1

var ErrUnsupportedVersion = errors.New("unsupported signal state version")

type Limits struct {
	Chats           int
	OwnerEvents     int
	ProcessedIDs    int
	OutboundIntents int
	EchoTTL         time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		Chats:           256,
		OwnerEvents:     256,
		ProcessedIDs:    2048,
		OutboundIntents: 256,
		EchoTTL:         10 * time.Minute,
	}
}

type ChatBinding struct {
	RunID     string    `json:"run_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OwnerEvent struct {
	ID              string    `json:"id"`
	ChatID          string    `json:"chat_id"`
	SignalMessageID string    `json:"signal_message_id,omitempty"`
	Text            string    `json:"text,omitempty"`
	Timestamp       int64     `json:"timestamp,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type ProcessedID struct {
	ID          string    `json:"id"`
	ProcessedAt time.Time `json:"processed_at"`
}

type OutboundIntent struct {
	ID            string    `json:"id"`
	ChatID        string    `json:"chat_id"`
	Text          string    `json:"text"`
	ContentHash   string    `json:"content_hash"`
	SentTimestamp int64     `json:"sent_timestamp,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type state struct {
	Version         int                    `json:"version"`
	Chats           map[string]ChatBinding `json:"chats,omitempty"`
	OwnerEvents     []OwnerEvent           `json:"pending_owner_events,omitempty"`
	ProcessedIDs    []ProcessedID          `json:"recent_processed_signal_ids,omitempty"`
	OutboundIntents []OutboundIntent       `json:"pending_outbound_intents,omitempty"`
}

type Store struct {
	mu              sync.Mutex
	path            string
	limits          Limits
	state           state
	quarantinedPath string
}

// Open loads path, creating it if needed. Invalid JSON is moved aside to a
// timestamped .corrupt file before an empty state is created. A state written
// by a newer implementation is not modified.
func Open(path string, limits Limits) (*Store, error) {
	limits = normalizedLimits(limits)
	s := &Store{path: path, limits: limits, state: newState()}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func OpenDefault(path string) (*Store, error) { return Open(path, DefaultLimits()) }

func (s *Store) Path() string { return s.path }

func (s *Store) QuarantinedPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quarantinedPath
}

func (s *Store) Binding(chatID string) (ChatBinding, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.state.Chats[chatID]
	return b, ok
}

func (s *Store) SetBinding(chatID, runID, sessionID string, now time.Time) error {
	if strings.TrimSpace(chatID) == "" {
		return errors.New("chat ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Chats[chatID] = ChatBinding{RunID: runID, SessionID: sessionID, UpdatedAt: now.UTC()}
	s.pruneLocked()
	return s.saveLocked()
}

func (s *Store) DeleteBinding(chatID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Chats[chatID]; !ok {
		return nil
	}
	delete(s.state.Chats, chatID)
	return s.saveLocked()
}

// ApplyOwnerEvent adds an event exactly once. Callers should supply an ID that
// is stable across retries (normally the Signal message ID). If omitted, a
// deterministic ID is derived from the event's source fields.
func (s *Store) ApplyOwnerEvent(event OwnerEvent) (bool, error) {
	if strings.TrimSpace(event.ChatID) == "" {
		return false, errors.New("owner event chat ID is required")
	}
	if event.ID == "" {
		event.ID = ownerEventID(event)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	} else {
		event.CreatedAt = event.CreatedAt.UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.state.OwnerEvents {
		if existing.ID == event.ID {
			return false, nil
		}
	}
	s.state.OwnerEvents = append(s.state.OwnerEvents, event)
	s.pruneLocked()
	return true, s.saveLocked()
}

func (s *Store) PendingOwnerEvents() []OwnerEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OwnerEvent(nil), s.state.OwnerEvents...)
}

func (s *Store) AckOwnerEvent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.OwnerEvents {
		if s.state.OwnerEvents[i].ID == id {
			s.state.OwnerEvents = append(s.state.OwnerEvents[:i], s.state.OwnerEvents[i+1:]...)
			return s.saveLocked()
		}
	}
	return nil
}

func (s *Store) IsProcessed(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.state.ProcessedIDs {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) MarkProcessed(id string, now time.Time) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, errors.New("processed Signal ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.state.ProcessedIDs {
		if item.ID == id {
			return false, nil
		}
	}
	s.state.ProcessedIDs = append(s.state.ProcessedIDs, ProcessedID{ID: id, ProcessedAt: now.UTC()})
	s.pruneLocked()
	return true, s.saveLocked()
}

// EnqueueOutbound adds a pending send exactly once. Empty IDs are generated
// randomly and returned so the caller can retain the stable identifier.
func (s *Store) EnqueueOutbound(intent OutboundIntent) (OutboundIntent, bool, error) {
	if strings.TrimSpace(intent.ChatID) == "" {
		return OutboundIntent{}, false, errors.New("outbound chat ID is required")
	}
	if intent.ID == "" {
		id, err := randomID()
		if err != nil {
			return OutboundIntent{}, false, err
		}
		intent.ID = id
	}
	intent.ContentHash = contentHash(intent.Text)
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = time.Now().UTC()
	} else {
		intent.CreatedAt = intent.CreatedAt.UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.state.OutboundIntents {
		if existing.ID == intent.ID {
			return existing, false, nil
		}
	}
	s.state.OutboundIntents = append(s.state.OutboundIntents, intent)
	s.pruneLocked()
	return intent, true, s.saveLocked()
}

func (s *Store) PendingOutbound() []OutboundIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]OutboundIntent(nil), s.state.OutboundIntents...)
}

func (s *Store) MarkOutboundSent(id string, sentTimestamp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.OutboundIntents {
		if s.state.OutboundIntents[i].ID != id {
			continue
		}
		if s.state.OutboundIntents[i].SentTimestamp == sentTimestamp {
			return nil
		}
		s.state.OutboundIntents[i].SentTimestamp = sentTimestamp
		return s.saveLocked()
	}
	return nil
}

func (s *Store) AckOutbound(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeOutboundLocked(id)
}

// MatchEcho acknowledges one outbound intent. An exact Signal timestamp wins,
// even when content is identical. If there is no exact timestamp, the oldest
// same-content intent inside EchoTTL is consumed (FIFO).
func (s *Store) MatchEcho(chatID, text string, signalTimestamp int64, now time.Time) (OutboundIntent, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if signalTimestamp != 0 {
		for _, intent := range s.state.OutboundIntents {
			if intent.ChatID == chatID && intent.SentTimestamp == signalTimestamp {
				return intent, true, s.removeOutboundLocked(intent.ID)
			}
		}
	}
	hash := contentHash(text)
	for _, intent := range s.state.OutboundIntents {
		if intent.ChatID != chatID || intent.ContentHash != hash {
			continue
		}
		age := now.Sub(intent.CreatedAt)
		if age >= 0 && age <= s.limits.EchoTTL {
			return intent, true, s.removeOutboundLocked(intent.ID)
		}
	}
	return OutboundIntent{}, false, nil
}

func (s *Store) load() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signal state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure signal state directory: %w", err)
	}
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.saveLocked()
	}
	if err != nil {
		return fmt.Errorf("open signal state: %w", err)
	}
	data, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return fmt.Errorf("read signal state: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close signal state: %w", closeErr)
	}
	var loaded state
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.Version <= 0 {
		return s.quarantineLocked()
	}
	if loaded.Version != CurrentVersion {
		return fmt.Errorf("%w: got %d, support %d", ErrUnsupportedVersion, loaded.Version, CurrentVersion)
	}
	if loaded.Chats == nil {
		loaded.Chats = make(map[string]ChatBinding)
	}
	s.state = loaded
	s.pruneLocked()
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("secure signal state file: %w", err)
	}
	return nil
}

func (s *Store) quarantineLocked() error {
	quarantine := fmt.Sprintf("%s.corrupt.%d", s.path, time.Now().UnixNano())
	if err := os.Rename(s.path, quarantine); err != nil {
		return fmt.Errorf("quarantine corrupt signal state: %w", err)
	}
	if err := os.Chmod(quarantine, 0o600); err != nil {
		return fmt.Errorf("secure quarantined signal state: %w", err)
	}
	s.quarantinedPath = quarantine
	s.state = newState()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode signal state: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create signal state directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure signal state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".signal-state-*")
	if err != nil {
		return fmt.Errorf("create signal state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure signal state temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write signal state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync signal state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close signal state: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace signal state: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) removeOutboundLocked(id string) error {
	for i := range s.state.OutboundIntents {
		if s.state.OutboundIntents[i].ID == id {
			s.state.OutboundIntents = append(s.state.OutboundIntents[:i], s.state.OutboundIntents[i+1:]...)
			return s.saveLocked()
		}
	}
	return nil
}

func (s *Store) pruneLocked() {
	trimOldest(&s.state.OwnerEvents, s.limits.OwnerEvents)
	trimOldest(&s.state.ProcessedIDs, s.limits.ProcessedIDs)
	trimOldest(&s.state.OutboundIntents, s.limits.OutboundIntents)
	if len(s.state.Chats) <= s.limits.Chats {
		return
	}
	type updatedChat struct {
		id string
		at time.Time
	}
	chats := make([]updatedChat, 0, len(s.state.Chats))
	for id, binding := range s.state.Chats {
		chats = append(chats, updatedChat{id: id, at: binding.UpdatedAt})
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].at.Before(chats[j].at) })
	for _, chat := range chats[:len(chats)-s.limits.Chats] {
		delete(s.state.Chats, chat.id)
	}
}

func trimOldest[T any](items *[]T, limit int) {
	if len(*items) > limit {
		*items = append([]T(nil), (*items)[len(*items)-limit:]...)
	}
}

func normalizedLimits(l Limits) Limits {
	d := DefaultLimits()
	if l.Chats <= 0 {
		l.Chats = d.Chats
	}
	if l.OwnerEvents <= 0 {
		l.OwnerEvents = d.OwnerEvents
	}
	if l.ProcessedIDs <= 0 {
		l.ProcessedIDs = d.ProcessedIDs
	}
	if l.OutboundIntents <= 0 {
		l.OutboundIntents = d.OutboundIntents
	}
	if l.EchoTTL <= 0 {
		l.EchoTTL = d.EchoTTL
	}
	return l
}

func newState() state {
	return state{Version: CurrentVersion, Chats: make(map[string]ChatBinding)}
}

func ownerEventID(event OwnerEvent) string {
	if event.SignalMessageID != "" {
		return "owner-" + contentHash(event.ChatID+"\x00"+event.SignalMessageID)
	}
	return "owner-" + contentHash(fmt.Sprintf("%s\x00%d\x00%s", event.ChatID, event.Timestamp, event.Text))
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate outbound intent ID: %w", err)
	}
	return "out-" + hex.EncodeToString(b[:]), nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open signal state directory for sync: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sync signal state directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close signal state directory: %w", closeErr)
	}
	return nil
}
