package gateway

import (
	"context"
	"time"

	"github.com/tripledoublev/v100/internal/acp"
)

// Update is a normalized inbound message from a chat transport.
type Update struct {
	ChatID    string
	MessageID string
	Text      string
	Images    []ImageAttachment
	Audio     *AudioAttachment
}

// ImageAttachment is a transport-normalized image payload.
type ImageAttachment struct {
	MIMEType string
	Data     []byte
	Path     string
}

// AudioAttachment is reserved for transports that can forward audio.
type AudioAttachment struct {
	MIMEType string
	Data     []byte
	Path     string
}

// Transport abstracts a chat platform such as Telegram or Signal.
type Transport interface {
	Name() string
	Poll(ctx context.Context) ([]Update, error)
	SendText(ctx context.Context, chatID string, chunks []string) error
	SendVoice(ctx context.Context, chatID, audioPath string) error
	SendTyping(ctx context.Context, chatID string) error
	React(ctx context.Context, chatID, messageID, emoji string) error
	Allowed(chatID string) bool
}

// VoiceConfig controls optional synthesized voice replies.
type VoiceConfig struct {
	Enabled bool
	Mode    string
}

// Config controls transport-agnostic gateway session behavior.
type Config struct {
	SessionIDPrefix string
	RunDir          string
	Workspace       string
	StreamResponses bool
	VoiceReplies    bool
	VoiceReplyMode  string
	StatusInterval  time.Duration
	PollRetryBase   time.Duration
	PollRetryMax    time.Duration
	ChunkChars      int
	BusyMessage     string
	PrepareSession  func(chatID string, params *acp.SessionNewParams) error
	BuildPrompt     func(workspace string, update Update) []acp.ContentBlock
	VoiceSettings   func(chatID string) VoiceConfig
}
