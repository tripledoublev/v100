package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	gatewaycore "github.com/tripledoublev/v100/internal/gateway"
	"github.com/tripledoublev/v100/internal/providers"
	"github.com/tripledoublev/v100/internal/signalstate"
	"github.com/tripledoublev/v100/internal/signalstyle"
)

const (
	signalChunkChars            = 3900
	signalBusyMessage           = "Still processing previous request. Please wait for my reply first."
	signalDefaultStatusInterval = 2 * time.Second
	signalPollRetryBase         = 1 * time.Second
	signalPollRetryMax          = 30 * time.Second
	signalShutdownTimeout       = 5 * time.Second
	signalManualContextMax      = 5
	signalGatewaySentWindow     = 5 * time.Minute
)

type signalRuntimeConfig struct {
	Account          string
	Socket           string
	TCP              string
	RPCMode          string
	ControlSocket    string
	RunDir           string
	Workspace        string
	StreamResponses  bool
	ConversationMode string
	MessageFormat    string
	BotPrefix        string
	StatePath        string
	VoiceReplies     bool
	VoiceReplyMode   string
	StatusInterval   time.Duration
	AllowedNumbers   map[string]struct{}
	Provider         string
	Profile          string
	ChatProfiles     map[string]string
	Profiles         map[string]config.GatewayProfile
	PromptBaseDir    string
}

type signalRPC interface {
	Receive(ctx context.Context) ([]signalReceiveEnvelope, error)
	Call(ctx context.Context, method string, params any, out any) error
	Close() error
}

type signalJSONRPC struct {
	conn    io.ReadWriteCloser
	account string
	mu      sync.Mutex
	nextID  int
}

type signalGateway struct {
	ctx       context.Context
	cfg       signalRuntimeConfig
	cfgMu     sync.RWMutex
	rpc       signalRPC
	cli       gatewaycore.ACPClient
	core      *gatewaycore.Core
	globalCfg *config.Config
	state     *signalstate.Store

	manualMu      sync.Mutex
	manualContext map[string][]signalManualContext
	manualPrompt  map[string]uint64
	manualNextID  uint64
	gatewaySent   map[string][]signalRecentSent
	styleMu       sync.Mutex
	styleDisabled bool
}

type signalManualContext struct {
	ID        uint64
	Text      string
	Timestamp string
}

type signalRecentSent struct {
	Text string
	At   time.Time
}

func gatewaySignalCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "signal",
		Short: "Run the Signal gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSignalGateway(cmd.Context(), cfgPath)
		},
	}
	cmd.AddCommand(gatewaySignalPromptCmd(cfgPath))
	cmd.AddCommand(gatewaySignalSendCmd(cfgPath))
	return cmd
}

func gatewaySignalPromptCmd(cfgPath *string) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "prompt --to NUMBER [message]",
		Short: "Run one ACP-backed Signal reply",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimSpace(string(b))
			}
			if strings.TrimSpace(to) == "" {
				return fmt.Errorf("--to is required")
			}
			if text == "" {
				return fmt.Errorf("message is required")
			}
			return runSignalGatewayControl(cmd.Context(), cfgPath, "prompt", to, text)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Signal recipient phone number")
	return cmd
}

func runSignalGateway(ctx context.Context, cfgPath *string) error {
	gw, stop, err := setupSignalGateway(ctx, cfgPath)
	if err != nil {
		return err
	}
	defer func() { _ = stop() }()
	return gw.gatewayCore().Run(ctx, gw)
}

func gatewaySignalSendCmd(cfgPath *string) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "send --to NUMBER [message]",
		Short: "Send one deterministic Signal message through the running gateway",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			if text == "" {
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = strings.TrimSpace(string(b))
			}
			if strings.TrimSpace(to) == "" {
				return fmt.Errorf("--to is required")
			}
			if text == "" {
				return fmt.Errorf("message is required")
			}
			return runSignalGatewayControl(cmd.Context(), cfgPath, "send", to, text)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Signal recipient phone number")
	return cmd
}

func runSignalGatewayControl(ctx context.Context, cfgPath *string, action, to, text string) error {
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	runtime := normalizeSignalConfig(cfg.Signal)
	socket := runtime.ControlSocket
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("connect signal gateway control socket %s: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if c, ok := conn.(interface{ SetDeadline(time.Time) error }); ok {
			_ = c.SetDeadline(deadline)
		}
	}
	req := signalControlRequest{
		Action: action,
		To:     strings.TrimSpace(to),
		Text:   text,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("send signal gateway control request: %w", err)
	}
	var res signalControlResponse
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return fmt.Errorf("read signal gateway control response: %w", err)
	}
	if !res.OK {
		if strings.TrimSpace(res.Error) == "" {
			return fmt.Errorf("signal gateway control request failed")
		}
		return fmt.Errorf("signal gateway control request failed: %s", res.Error)
	}
	if strings.TrimSpace(res.Warning) != "" {
		fmt.Fprintln(os.Stderr, "warning:", res.Warning)
	}
	return nil
}

func setupSignalGateway(ctx context.Context, cfgPath *string) (*signalGateway, func() error, error) {
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return nil, nil, err
	}
	normalized := normalizeSignalConfig(cfg.Signal)
	if signalstyle.UTF16Len(normalized.BotPrefix) >= signalChunkChars {
		return nil, nil, fmt.Errorf("signal bot_prefix must be shorter than %d UTF-16 code units", signalChunkChars)
	}
	normalized.Profiles = cfg.Gateway.Profiles
	normalized.PromptBaseDir = cfg.PromptBaseDir()
	if !cfg.Signal.Enabled {
		return nil, nil, fmt.Errorf("signal gateway is disabled")
	}
	rpc, err := newSignalRPC(ctx, normalized)
	if err != nil {
		return nil, nil, err
	}
	gw := &signalGateway{ctx: ctx, cfg: normalized, rpc: rpc, globalCfg: cfg}
	if normalized.ConversationMode == "shared_account" {
		state, stateErr := signalstate.OpenDefault(normalized.StatePath)
		if stateErr != nil {
			_ = rpc.Close()
			return nil, nil, fmt.Errorf("open Signal gateway state: %w", stateErr)
		}
		gw.state = state
	}
	proc, err := gatewaycore.StartACPProcess(ctx, gatewaycore.ACPProcessOptions{
		ConfigPath:      *cfgPath,
		Provider:        normalized.Provider,
		ShutdownTimeout: signalShutdownTimeout,
		OnNotification: func(note acp.Notification) {
			_ = gw.gatewayCore().HandleNotification(ctx, gw, note)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	gw.cli = proc.Client
	stopControl, err := startSignalControlServer(ctx, gw)
	if err != nil {
		_ = proc.Stop()
		_ = rpc.Close()
		return nil, nil, err
	}
	return gw, func() error {
		if stopControl != nil {
			_ = stopControl()
		}
		_ = gw.gatewayCore().CloseAllSessions(context.Background())
		procErr := proc.Stop()
		rpcErr := rpc.Close()
		if procErr != nil {
			return procErr
		}
		return rpcErr
	}, nil
}

func normalizeSignalConfig(cfg config.SignalConfig) signalRuntimeConfig {
	statusInterval := time.Duration(cfg.StatusIntervalSec) * time.Second
	if statusInterval <= 0 {
		statusInterval = signalDefaultStatusInterval
	}
	allowed := make(map[string]struct{}, len(cfg.AllowedNumbers))
	for _, number := range cfg.AllowedNumbers {
		number = strings.TrimSpace(number)
		if number != "" {
			allowed[number] = struct{}{}
		}
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.RPCMode))
	if mode == "" {
		mode = "socket"
	}
	conversationMode := normalizeSignalConversationMode(cfg.ConversationMode)
	botPrefix := cfg.BotPrefix
	if conversationMode == "shared_account" && botPrefix == "" {
		botPrefix = "🤖 "
	}
	controlSocket := normalizeSignalControlSocket(cfg.ControlSocket, cfg.Account)
	statePath := strings.TrimSpace(cfg.StatePath)
	if statePath == "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(cfg.Account)))
		statePath = filepath.Join(config.UserDataDir(), "signal", hex.EncodeToString(sum[:8])+".json")
	}
	return signalRuntimeConfig{
		Account:          strings.TrimSpace(cfg.Account),
		Socket:           strings.TrimSpace(cfg.Socket),
		TCP:              strings.TrimSpace(cfg.TCP),
		RPCMode:          mode,
		ControlSocket:    controlSocket,
		RunDir:           strings.TrimSpace(cfg.RunDir),
		Workspace:        strings.TrimSpace(cfg.Workspace),
		StreamResponses:  cfg.StreamResponses,
		ConversationMode: conversationMode,
		MessageFormat:    normalizeSignalMessageFormat(cfg.MessageFormat),
		BotPrefix:        botPrefix,
		StatePath:        statePath,
		VoiceReplies:     cfg.VoiceReplies,
		VoiceReplyMode:   strings.TrimSpace(cfg.VoiceReplyMode),
		StatusInterval:   statusInterval,
		AllowedNumbers:   allowed,
		Provider:         strings.TrimSpace(cfg.Provider),
		Profile:          strings.TrimSpace(cfg.Profile),
		ChatProfiles:     copyStringMap(cfg.ChatProfiles),
	}
}

func normalizeSignalConversationMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "legacy"
	}
	return mode
}

func normalizeSignalMessageFormat(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return "plain"
	}
	return format
}

type signalControlRequest struct {
	Action string `json:"action,omitempty"`
	To     string `json:"to"`
	Text   string `json:"text"`
}

type signalControlResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

func startSignalControlServer(ctx context.Context, gw *signalGateway) (func() error, error) {
	if gw == nil {
		return nil, fmt.Errorf("signal gateway is required")
	}
	socket := strings.TrimSpace(gw.cfg.ControlSocket)
	if socket == "" {
		return nil, fmt.Errorf("signal control socket is required")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, fmt.Errorf("create signal control socket dir: %w", err)
	}
	if err := prepareSignalControlSocket(socket); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen signal control socket: %w", err)
	}
	_ = os.Chmod(socket, 0o600)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go gw.handleSignalControlConn(ctx, conn)
		}
	}()
	log.Printf("signal control socket listening at %s", socket)
	return func() error {
		err := ln.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		_ = os.Remove(socket)
		return err
	}, nil
}

func prepareSignalControlSocket(socket string) error {
	info, err := os.Stat(socket)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat signal control socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("signal control socket path exists and is not a socket: %s", socket)
	}
	conn, dialErr := net.DialTimeout("unix", socket, 200*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("signal control socket already has a listener: %s", socket)
	}
	if err := os.Remove(socket); err != nil {
		return fmt.Errorf("remove stale signal control socket: %w", err)
	}
	return nil
}

func (g *signalGateway) handleSignalControlConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	var req signalControlRequest
	res := signalControlResponse{OK: true}
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		res = signalControlResponse{Error: "invalid request: " + err.Error()}
		_ = json.NewEncoder(conn).Encode(res)
		return
	}
	to := strings.TrimSpace(req.To)
	text := strings.TrimSpace(req.Text)
	switch {
	case to == "":
		res = signalControlResponse{Error: "to is required"}
	case text == "":
		res = signalControlResponse{Error: "text is required"}
	case !g.Allowed(to):
		res = signalControlResponse{Error: "recipient is not allowed"}
	default:
		action := strings.ToLower(strings.TrimSpace(req.Action))
		if action == "" {
			action = "prompt"
		}
		var err error
		switch action {
		case "prompt":
			err = g.gatewayCore().Handle(ctx, g, gatewaycore.Update{
				ChatID:    to,
				MessageID: fmt.Sprintf("terminal-%d", time.Now().UnixMilli()),
				Text:      text,
			})
		case "send":
			eventID := fmt.Sprintf("signal-gateway-system-%d", time.Now().UnixNano())
			if g.state != nil {
				_, err = g.ensureSignalSession(ctx, to)
			}
			if err == nil {
				err = g.SendFormattedText(ctx, to, text)
				var deliveredErr *signalDeliveredStateError
				if errors.As(err, &deliveredErr) {
					res.Warning = deliveredErr.Error()
					err = nil
				}
			}
			if err == nil && g.state != nil {
				if _, appendErr := g.gatewayCore().AppendContext(ctx, to, "assistant", "signal.gateway_system", eventID, text); appendErr != nil {
					warning := fmt.Sprintf("message was delivered but its trace append failed; do not retry automatically: %v", appendErr)
					if res.Warning != "" {
						res.Warning += "; " + warning
					} else {
						res.Warning = warning
					}
				}
			}
		default:
			err = fmt.Errorf("unsupported action %q", action)
		}
		if err != nil {
			res = signalControlResponse{Error: err.Error()}
		}
	}
	_ = json.NewEncoder(conn).Encode(res)
}

func normalizeSignalControlSocket(path, account string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		return path
	}
	base := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if base == "" {
		base = os.TempDir()
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(account)))
	return filepath.Join(base, "v100-signal-"+hex.EncodeToString(sum[:8])+".sock")
}

func (g *signalGateway) Name() string { return "signal" }

func (g *signalGateway) gatewayCore() *gatewaycore.Core {
	if g.core != nil {
		return g.core
	}
	var streamSplitter gatewaycore.StreamSplitter
	if g.cfg.MessageFormat == "signal_markdown" {
		streamSplitter = signalstyle.SplitStream
	}
	g.core = gatewaycore.NewCore(gatewaycore.Config{
		SessionIDPrefix: "signal-",
		RunDir:          g.cfg.RunDir,
		Workspace:       g.cfg.Workspace,
		StreamResponses: g.cfg.StreamResponses,
		VoiceReplies:    g.cfg.VoiceReplies,
		VoiceReplyMode:  g.cfg.VoiceReplyMode,
		StatusInterval:  g.cfg.StatusInterval,
		PollRetryBase:   signalPollRetryBase,
		PollRetryMax:    signalPollRetryMax,
		ChunkChars:      signalChunkChars,
		BusyMessage:     signalBusyMessage,
		StreamSplitter:  streamSplitter,
		PrepareSession: func(chatID string, params *acp.SessionNewParams) error {
			return gatewaycore.ApplyProfileToSessionNew(params, g.effectiveGatewayProfile(chatID), g.cfg.PromptBaseDir)
		},
		PrepareResume: func(chatID string, params *acp.SessionResumeParams) error {
			return gatewaycore.ApplyProfileToSessionResume(params, g.effectiveGatewayProfile(chatID), g.cfg.PromptBaseDir)
		},
		VoiceSettings: func(chatID string) gatewaycore.VoiceConfig {
			return gatewayVoiceConfig(g.cfg.VoiceReplies, g.cfg.VoiceReplyMode, g.effectiveGatewayProfile(chatID))
		},
		BuildPrompt: g.buildSignalPrompt,
		AfterPrompt: g.afterSignalPrompt,
	}, g.cli)
	return g.core
}

func (g *signalGateway) Poll(ctx context.Context) ([]gatewaycore.Update, error) {
	if err := g.flushPendingOwnerEvents(ctx); err != nil {
		return nil, err
	}
	envelopes, err := g.rpc.Receive(ctx)
	if err != nil {
		if isSignalRPCClosedError(err) {
			return nil, gatewaycore.FatalPoll(err)
		}
		return nil, err
	}
	updates := make([]gatewaycore.Update, 0, len(envelopes))
	for _, env := range envelopes {
		handledSync, syncErr := g.recordSignalSentSync(ctx, env.Envelope)
		if syncErr != nil {
			return nil, syncErr
		}
		if handledSync {
			continue
		}
		number := strings.TrimSpace(env.Envelope.Source)
		if sourceNumber := strings.TrimSpace(env.Envelope.SourceNumber); sourceNumber != "" {
			number = sourceNumber
		}
		if number == "" || !g.Allowed(number) {
			continue
		}
		rawMsg := ""
		msg := ""
		if env.Envelope.DataMessage != nil {
			rawMsg = strings.TrimSpace(env.Envelope.DataMessage.Message)
			msg = g.signalDataMessageText(env.Envelope.DataMessage)
		}
		var images []gatewaycore.ImageAttachment
		if env.Envelope.DataMessage != nil {
			images = signalImageAttachments(env.Envelope.DataMessage)
		}
		if rawMsg == "" && msg == "" && len(images) == 0 {
			continue
		}
		if msg == "" && len(images) > 0 {
			msg = "User sent an image."
		}
		if command, ok := gatewaycore.ParseCommand(rawMsg); ok {
			if err := g.handleSignalCommand(ctx, number, command); err != nil {
				return nil, err
			}
			continue
		}
		msgID := signalTimestampString(env.Envelope.Timestamp)
		if g.state != nil && msgID != "" {
			if g.state.IsProcessed(msgID) {
				continue
			}
			if _, err := g.ensureSignalSession(ctx, number); err != nil {
				return nil, err
			}
		}
		log.Printf("signal message accepted from %s message_id=%s chars=%d", redactSignalChatID(number), msgID, len([]rune(msg)))
		go func(cID, mID, text string) {
			if emoji := g.chooseReaction(ctx, cID, text); emoji != "" {
				_ = g.React(ctx, cID, mID, emoji)
			}
		}(number, msgID, msg)
		updates = append(updates, gatewaycore.Update{
			ChatID:          number,
			MessageID:       msgID,
			Text:            msg,
			Images:          images,
			Source:          signalSource(g.state != nil, "signal.friend"),
			ResponseSource:  signalSource(g.state != nil, "signal.bot"),
			ExternalEventID: signalExternalEventID(g.state != nil, "friend", msgID),
		})
	}
	return updates, nil
}

// signalAttachmentsDir returns the directory signal-cli stores received
// attachment files in, keyed by attachment ID.
func signalAttachmentsDir() string {
	if base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); base != "" {
		return filepath.Join(base, "signal-cli", "attachments")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "signal-cli", "attachments")
}

// signalImageAttachments reads any image attachments on a data message off
// disk and converts them to gateway.ImageAttachment. Non-image attachments
// (voice notes, files, etc.) are skipped; a missing or unreadable file is
// logged and skipped rather than failing the whole message.
func signalImageAttachments(msg *signalDataMessage) []gatewaycore.ImageAttachment {
	if msg == nil || len(msg.Attachments) == 0 {
		return nil
	}
	dir := signalAttachmentsDir()
	var out []gatewaycore.ImageAttachment
	for _, att := range msg.Attachments {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(att.ContentType)), "image/") {
			continue
		}
		id := strings.TrimSpace(att.ID)
		if id == "" || dir == "" {
			continue
		}
		path := filepath.Join(dir, id)
		data, err := os.ReadFile(path)
		if err != nil {
			// signal-cli stores attachments with a content-type extension
			// appended (e.g. "<id>.jpg"), not the bare ID; fall back to a glob.
			if matches, globErr := filepath.Glob(filepath.Join(dir, id+".*")); globErr == nil && len(matches) > 0 {
				path = matches[0]
				data, err = os.ReadFile(path)
			}
		}
		if err != nil {
			log.Printf("signal attachment read error id=%s: %v", id, err)
			continue
		}
		out = append(out, gatewaycore.ImageAttachment{
			MIMEType: att.ContentType,
			Data:     data,
			Path:     path,
		})
	}
	return out
}

func isSignalRPCClosedError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, os.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET)
}

func (g *signalGateway) recordSignalSentSync(ctx context.Context, env signalEnvelope) (bool, error) {
	if env.SyncMessage == nil || env.SyncMessage.SentMessage == nil {
		return false, nil
	}
	sent := env.SyncMessage.SentMessage
	number := strings.TrimSpace(sent.Destination)
	if destinationNumber := strings.TrimSpace(sent.DestinationNumber); destinationNumber != "" {
		number = destinationNumber
	}
	rawText := strings.TrimSpace(sent.Message)
	text := g.signalSentMessageText(sent)
	if number == "" || text == "" || !g.Allowed(number) {
		return true, nil
	}
	if g.state != nil && rawText != "" {
		_, matched, err := g.state.MatchEcho(number, rawText, signalTimestampInt64(sent.Timestamp), time.Now())
		if err != nil {
			return true, fmt.Errorf("match Signal sent echo: %w", err)
		}
		if matched {
			return true, nil
		}
	}
	if rawText != "" && g.consumeGatewaySent(number, rawText) {
		return true, nil
	}
	if g.state != nil {
		timestamp := signalTimestampInt64(sent.Timestamp)
		messageID := signalTimestampString(sent.Timestamp)
		eventID := signalExternalEventID(true, "owner", messageID)
		if eventID == "" {
			sum := sha256.Sum256([]byte(number + "\x00" + text))
			eventID = "signal-owner-hash-" + hex.EncodeToString(sum[:16])
		}
		event := signalstate.OwnerEvent{
			ID:              eventID,
			ChatID:          number,
			SignalMessageID: messageID,
			Text:            text,
			Timestamp:       timestamp,
		}
		_, err := g.state.ApplyOwnerEvent(event)
		if err != nil {
			return true, fmt.Errorf("persist Signal owner message: %w", err)
		}
		if err := g.flushPendingOwnerEvents(ctx); err != nil {
			return true, err
		}
		log.Printf("signal owner context appended for %s chars=%d", redactSignalChatID(number), len([]rune(text)))
		return true, nil
	}
	g.appendManualContext(number, signalManualContext{
		Text:      text,
		Timestamp: signalTimestampString(sent.Timestamp),
	})
	log.Printf("signal manual sent context recorded for %s chars=%d", redactSignalChatID(number), len([]rune(text)))
	return true, nil
}

func signalSource(enabled bool, source string) string {
	if !enabled {
		return ""
	}
	return source
}

func signalExternalEventID(enabled bool, kind, messageID string) string {
	if !enabled || strings.TrimSpace(messageID) == "" {
		return ""
	}
	return "signal-" + kind + "-" + strings.TrimSpace(messageID)
}

func (g *signalGateway) ensureSignalSession(ctx context.Context, chatID string) (*gatewaycore.Session, error) {
	core := g.gatewayCore()
	if existing, ok := core.SessionInfo(chatID); ok {
		return &gatewaycore.Session{ChatID: existing.ChatID, SessionID: existing.SessionID, RunID: existing.RunID}, nil
	}
	if g.state != nil {
		if binding, ok := g.state.Binding(chatID); ok && strings.TrimSpace(binding.RunID) != "" {
			state, err := core.ResumeSession(ctx, chatID, binding.RunID)
			if err != nil {
				if !isACPSessionNotFound(err) {
					return nil, err
				}
				log.Printf("signal stored run missing for %s; starting a fresh session", redactSignalChatID(chatID))
				if clearErr := g.state.DeleteBinding(chatID); clearErr != nil {
					return nil, fmt.Errorf("clear stale Signal session binding: %w", clearErr)
				}
			} else {
				if err := g.state.SetBinding(chatID, state.RunID, state.SessionID, time.Now()); err != nil {
					return nil, fmt.Errorf("update resumed Signal binding: %w", err)
				}
				return state, nil
			}
		}
	}
	state, err := core.GetOrCreateSession(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if g.state != nil {
		if err := g.state.SetBinding(chatID, state.RunID, state.SessionID, time.Now()); err != nil {
			return nil, fmt.Errorf("persist Signal session binding: %w", err)
		}
	}
	return state, nil
}

func isACPSessionNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), fmt.Sprintf("%d:", acp.ErrSessionNotFound))
}

func (g *signalGateway) flushPendingOwnerEvents(ctx context.Context) error {
	if g.state == nil {
		return nil
	}
	for _, event := range g.state.PendingOwnerEvents() {
		if _, err := g.ensureSignalSession(ctx, event.ChatID); err != nil {
			return err
		}
		if _, err := g.gatewayCore().AppendContext(ctx, event.ChatID, "assistant", "signal.owner_manual", event.ID, event.Text); err != nil {
			return err
		}
		if err := g.state.AckOwnerEvent(event.ID); err != nil {
			return fmt.Errorf("ack Signal owner context: %w", err)
		}
	}
	return nil
}

func (g *signalGateway) appendManualContext(chatID string, entry signalManualContext) {
	chatID = strings.TrimSpace(chatID)
	entry.Text = strings.TrimSpace(entry.Text)
	if chatID == "" || entry.Text == "" {
		return
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	if g.manualContext == nil {
		g.manualContext = map[string][]signalManualContext{}
	}
	if entry.ID == 0 {
		g.manualNextID++
		entry.ID = g.manualNextID
	}
	items := append(g.manualContext[chatID], entry)
	if len(items) > signalManualContextMax {
		items = items[len(items)-signalManualContextMax:]
	}
	g.manualContext[chatID] = items
}

func (g *signalGateway) peekManualContext(chatID string) []signalManualContext {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	return append([]signalManualContext(nil), g.manualContext[chatID]...)
}

func (g *signalGateway) drainManualContext(chatID string) []signalManualContext {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	items := append([]signalManualContext(nil), g.manualContext[chatID]...)
	delete(g.manualContext, chatID)
	return items
}

func (g *signalGateway) clearManualContext(chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	delete(g.manualContext, chatID)
	delete(g.manualPrompt, chatID)
}

func (g *signalGateway) rememberGatewaySent(chatID, text string) {
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return
	}
	now := time.Now()
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	if g.gatewaySent == nil {
		g.gatewaySent = map[string][]signalRecentSent{}
	}
	items := pruneRecentSignalSent(g.gatewaySent[chatID], now)
	items = append(items, signalRecentSent{Text: text, At: now})
	g.gatewaySent[chatID] = items
}

func (g *signalGateway) consumeGatewaySent(chatID, text string) bool {
	chatID = strings.TrimSpace(chatID)
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return false
	}
	now := time.Now()
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	items := pruneRecentSignalSent(g.gatewaySent[chatID], now)
	for i, item := range items {
		if item.Text == text {
			items = append(items[:i], items[i+1:]...)
			if len(items) == 0 {
				delete(g.gatewaySent, chatID)
			} else {
				g.gatewaySent[chatID] = items
			}
			return true
		}
	}
	if len(items) == 0 {
		delete(g.gatewaySent, chatID)
	} else {
		g.gatewaySent[chatID] = items
	}
	return false
}

func pruneRecentSignalSent(items []signalRecentSent, now time.Time) []signalRecentSent {
	if len(items) == 0 {
		return nil
	}
	out := items[:0]
	for _, item := range items {
		if now.Sub(item.At) <= signalGatewaySentWindow {
			out = append(out, item)
		}
	}
	return out
}

func (g *signalGateway) signalDataMessageText(msg *signalDataMessage) string {
	if msg == nil {
		return ""
	}
	return g.signalMessageTextWithQuote(msg.Message, msg.Quote, "signal.friend")
}

func (g *signalGateway) signalSentMessageText(msg *signalSentMessage) string {
	if msg == nil {
		return ""
	}
	return g.signalMessageTextWithQuote(msg.Message, msg.Quote, "signal.owner_manual")
}

func (g *signalGateway) signalMessageTextWithQuote(message string, quote *signalQuote, actor string) string {
	message = strings.TrimSpace(message)
	if quote == nil {
		return message
	}
	quoted := strings.TrimSpace(quote.Text)
	if quoted == "" {
		return message
	}
	if message == "" {
		message = "(no text)"
	}
	target := "signal.friend (the friend)"
	author := strings.TrimSpace(quote.AuthorNumber)
	if author == "" {
		author = strings.TrimSpace(quote.Author)
	}
	if author == g.cfg.Account {
		target = "signal.owner_manual (Vincent)"
		if prefix := g.cfg.BotPrefix; prefix != "" && strings.HasPrefix(quoted, prefix) {
			target = "signal.bot (v100)"
		}
	}
	return fmt.Sprintf("%s replied to %s:\n\n%s\n\nwith:\n\n%s", actor, target, quoted, message)
}

func (g *signalGateway) buildSignalPrompt(_ string, update gatewaycore.Update) []acp.ContentBlock {
	text := strings.TrimSpace(update.Text)
	manual := g.peekManualContext(update.ChatID)
	g.rememberSignalPromptManualMax(update.ChatID, manual)
	if len(manual) == 0 {
		return signalPromptWithImages(text, update.Images)
	}
	var b strings.Builder
	b.WriteString("Context: the account owner manually sent these Signal messages in this chat since your last turn. Treat them as already-sent human messages from this same account. Do not reply to these context messages directly.\n")
	for _, entry := range manual {
		if strings.TrimSpace(entry.Timestamp) != "" {
			fmt.Fprintf(&b, "- [%s] %s\n", entry.Timestamp, entry.Text)
			continue
		}
		fmt.Fprintf(&b, "- %s\n", entry.Text)
	}
	if text != "" {
		b.WriteString("\nLatest incoming message:\n")
		b.WriteString(text)
	}
	return signalPromptWithImages(strings.TrimSpace(b.String()), update.Images)
}

// signalPromptWithImages builds the ACP content blocks for a Signal turn,
// attaching any image attachments alongside the text block. Mirrors the
// generic gateway.updatePrompt image handling so Signal doesn't silently
// drop images the way buildSignalPrompt used to.
func signalPromptWithImages(text string, images []gatewaycore.ImageAttachment) []acp.ContentBlock {
	blocks := []acp.ContentBlock{{Type: "text", Text: text}}
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(img.MIMEType)
		if mimeType == "" {
			mimeType = "image/jpeg"
		}
		blocks = append(blocks, acp.ContentBlock{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(img.Data),
			MimeType: mimeType,
		})
	}
	return blocks
}

func (g *signalGateway) rememberSignalPromptManualMax(chatID string, manual []signalManualContext) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	var maxID uint64
	for _, entry := range manual {
		if entry.ID > maxID {
			maxID = entry.ID
		}
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	if maxID == 0 {
		delete(g.manualPrompt, chatID)
		return
	}
	if g.manualPrompt == nil {
		g.manualPrompt = map[string]uint64{}
	}
	g.manualPrompt[chatID] = maxID
}

func (g *signalGateway) afterSignalPrompt(_ string, update gatewaycore.Update) error {
	if g.state != nil && strings.TrimSpace(update.MessageID) != "" {
		if _, err := g.state.MarkProcessed(update.MessageID, time.Now()); err != nil {
			return fmt.Errorf("record processed Signal message: %w", err)
		}
	}
	g.clearDeliveredManualContext(update.ChatID)
	return nil
}

func (g *signalGateway) clearDeliveredManualContext(chatID string) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return
	}
	g.manualMu.Lock()
	defer g.manualMu.Unlock()
	maxID := g.manualPrompt[chatID]
	delete(g.manualPrompt, chatID)
	if maxID == 0 {
		return
	}
	items := g.manualContext[chatID]
	out := items[:0]
	for _, item := range items {
		if item.ID > maxID {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		delete(g.manualContext, chatID)
		return
	}
	g.manualContext[chatID] = out
}

func (g *signalGateway) SendText(ctx context.Context, chatID string, chunks []string) error {
	maxContent := signalChunkChars - signalstyle.UTF16Len(g.cfg.BotPrefix)
	if maxContent <= 0 {
		return fmt.Errorf("signal bot prefix leaves no room for message content")
	}
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		for _, part := range signalstyle.Chunk(signalstyle.Result{Text: chunk}, maxContent) {
			if err := g.sendSignalResult(ctx, chatID, part); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendFormattedText renders a complete assistant fragment to Signal-native
// style ranges before chunking. That keeps UTF-16 offsets correct per message.
func (g *signalGateway) SendFormattedText(ctx context.Context, chatID, source string) error {
	result := signalstyle.Result{Text: source}
	if g.cfg.MessageFormat == "signal_markdown" {
		result = signalstyle.Render(source)
	}
	maxContent := signalChunkChars - signalstyle.UTF16Len(g.cfg.BotPrefix)
	if maxContent <= 0 {
		return fmt.Errorf("signal bot prefix leaves no room for message content")
	}
	for _, chunk := range signalstyle.Chunk(result, maxContent) {
		if err := g.sendSignalResult(ctx, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (g *signalGateway) sendSignalResult(ctx context.Context, chatID string, result signalstyle.Result) error {
	prefix := g.cfg.BotPrefix
	if prefix != "" {
		offset := signalstyle.UTF16Len(prefix)
		result.Text = prefix + result.Text
		for i := range result.Styles {
			result.Styles[i].Start += offset
		}
	}
	params := map[string]any{
		"account":   g.cfg.Account,
		"recipient": chatID,
		"message":   result.Text,
	}
	var intent signalstate.OutboundIntent
	if g.state != nil {
		var err error
		intent, _, err = g.state.EnqueueOutbound(signalstate.OutboundIntent{ChatID: chatID, Text: result.Text})
		if err != nil {
			return fmt.Errorf("persist Signal outbound intent: %w", err)
		}
	}
	g.styleMu.Lock()
	styleDisabled := g.styleDisabled
	g.styleMu.Unlock()
	if !styleDisabled && len(result.Styles) > 0 {
		styles := make([]string, 0, len(result.Styles))
		for _, style := range result.Styles {
			styles = append(styles, style.String())
		}
		params["textStyle"] = styles
	}
	var sentResult struct {
		Timestamp any `json:"timestamp"`
	}
	err := g.rpc.Call(ctx, "send", params, &sentResult)
	if err != nil && params["textStyle"] != nil && isSignalTextStyleUnsupported(err) {
		g.styleMu.Lock()
		g.styleDisabled = true
		g.styleMu.Unlock()
		delete(params, "textStyle")
		err = g.rpc.Call(ctx, "send", params, &sentResult)
	}
	if err != nil {
		if g.state != nil {
			var rpcErr *signalRPCError
			if errors.As(err, &rpcErr) {
				_ = g.state.AckOutbound(intent.ID)
			} else if stateErr := g.state.MarkOutboundPossiblySent(intent.ID); stateErr != nil {
				return fmt.Errorf("signal send failed (%v) and ambiguous outcome could not be recorded: %w", err, stateErr)
			}
		}
		return err
	}
	g.rememberGatewaySent(chatID, result.Text)
	if g.state != nil {
		if err := g.state.MarkOutboundSent(intent.ID, signalTimestampInt64(sentResult.Timestamp)); err != nil {
			return &signalDeliveredStateError{Err: fmt.Errorf("record Signal outbound timestamp: %w", err)}
		}
	}
	return nil
}

type signalDeliveredStateError struct{ Err error }

func (e *signalDeliveredStateError) Error() string {
	return fmt.Sprintf("message was delivered but delivery state persistence failed; do not retry automatically: %v", e.Err)
}

func (e *signalDeliveredStateError) Unwrap() error { return e.Err }

func isSignalTextStyleUnsupported(err error) bool {
	var rpcErr *signalRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	message := strings.ToLower(rpcErr.Message)
	return rpcErr.Code == -32602 ||
		(strings.Contains(message, "textstyle") &&
			(strings.Contains(message, "unknown") || strings.Contains(message, "unsupported") || strings.Contains(message, "invalid")))
}

func (g *signalGateway) SendVoice(context.Context, string, string) error {
	// signal-cli attachment sending is intentionally left as a no-op for the
	// first voice-reply pass; text fallback remains available through the core.
	return nil
}

func (g *signalGateway) SendTyping(ctx context.Context, chatID string) error {
	return g.rpc.Call(ctx, "sendTyping", map[string]any{
		"account":   g.cfg.Account,
		"recipient": chatID,
	}, nil)
}

func (g *signalGateway) React(ctx context.Context, chatID, messageID, emoji string) error {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	targetTimestamp, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil {
		return nil
	}
	_ = g.rpc.Call(ctx, "sendReaction", map[string]any{
		"account":         g.cfg.Account,
		"recipient":       chatID,
		"targetAuthor":    chatID,
		"targetTimestamp": targetTimestamp,
		"emoji":           emoji,
	}, nil)
	return nil
}

func (g *signalGateway) chooseReaction(ctx context.Context, chatID, text string) string {
	rt := g.effectiveGatewayProfile(chatID)
	profile := rt.Profile
	mode := strings.ToLower(strings.TrimSpace(profile.ReactionMode))
	emojis := profile.ReactionEmojis

	switch mode {
	case "none":
		return ""
	case "random":
		if len(emojis) == 0 {
			return "👍"
		}
		return emojis[rand.Intn(len(emojis))]
	case "smart":
		if len(emojis) == 0 {
			emojis = []string{"👍", "❤️", "😂", "🤔", "👀"}
		}
		ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		providerName := profile.Provider
		if providerName == "" {
			providerName = g.globalCfg.Defaults.Provider
		}
		prov, err := buildProvider(g.globalCfg, providerName)
		if err == nil {
			req := providers.CompleteRequest{
				Model: profile.Model,
				Messages: []providers.Message{
					{Role: "system", Content: "Pick one emoji to react to this message. Reply ONLY with the single emoji. Do not use quotes. Options: " + strings.Join(emojis, " ")},
					{Role: "user", Content: text},
				},
				GenParams: providers.GenParams{MaxTokens: 10},
			}
			res, err := prov.Complete(ctxTimeout, req)
			if err == nil {
				out := strings.TrimSpace(res.AssistantText)
				for _, e := range emojis {
					if strings.Contains(out, e) {
						return e
					}
				}
			}
		}
		return emojis[0]
	default:
		if len(emojis) > 0 {
			return emojis[0]
		}
		return "👍"
	}
}

func (g *signalGateway) commandAllowed(chatID, command string) bool {
	runtime := g.effectiveGatewayProfile(chatID)
	return gatewaycore.CommandAllowed(runtime.Profile, strings.TrimSpace(runtime.Name) != "", command)
}

func (g *signalGateway) Allowed(chatID string) bool {
	if len(g.cfg.AllowedNumbers) == 0 {
		return true
	}
	_, ok := g.cfg.AllowedNumbers[strings.TrimSpace(chatID)]
	return ok
}

func (g *signalGateway) effectiveGatewayProfile(chatID string) gatewaycore.ProfileRuntime {
	g.cfgMu.RLock()
	chatProfiles := copyStringMap(g.cfg.ChatProfiles)
	g.cfgMu.RUnlock()
	return gatewaycore.ResolveProfile(g.cfg.Profiles, g.cfg.Profile, chatProfiles, chatID)
}

func (g *signalGateway) handleSignalCommand(ctx context.Context, number string, command gatewaycore.Command) error {
	if !g.commandAllowed(number, command.Name) {
		return g.SendText(ctx, number, []string{fmt.Sprintf("Command /%s is not allowed for this chat.", command.Name)})
	}
	switch command.Name {
	case "help":
		return g.SendText(ctx, number, []string{g.signalCommandHelp(number)})
	case "whoami":
		return g.SendText(ctx, number, []string{g.signalCommandWhoami(number)})
	case "status":
		return g.SendText(ctx, number, []string{g.signalCommandStatus(number)})
	case "provider", "model", "solver":
		return g.reconfigureSignalSession(ctx, number, command)
	case "profile":
		return g.switchSignalProfile(ctx, number, command.Arg)
	case "reset":
		return g.resetSignalSession(ctx, number)
	default:
		return g.SendText(ctx, number, []string{fmt.Sprintf("Unknown command /%s. Try /help.", command.Name)})
	}
}

func (g *signalGateway) signalCommandHelp(chatID string) string {
	commands := []string{"help", "whoami", "status", "reset"}
	if runtime := g.effectiveGatewayProfile(chatID); runtime.OK {
		commands = runtime.Profile.AllowedCommands
	}
	if len(commands) == 0 {
		return "No commands are enabled for this chat."
	}
	var b strings.Builder
	b.WriteString("Available commands:")
	for _, command := range commands {
		command = gatewaycore.NormalizeCommandName(command)
		if command == "" {
			continue
		}
		fmt.Fprintf(&b, "\n/%s", command)
	}
	return b.String()
}

func (g *signalGateway) signalCommandWhoami(chatID string) string {
	runtime := g.effectiveGatewayProfile(chatID)
	profileName := runtime.Name
	if !runtime.OK {
		profileName = "(none)"
	}
	profile := runtime.Profile
	sessionID := "(none)"
	if info, ok := g.gatewayCore().SessionInfo(chatID); ok {
		sessionID = info.SessionID
	}
	provider := strings.TrimSpace(profile.Provider)
	if provider == "" {
		provider = "(default)"
	}
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		model = "(default)"
	}
	solver := strings.TrimSpace(profile.Solver)
	if solver == "" {
		solver = "(default)"
	}
	return fmt.Sprintf("ChatID: %s\nProfile: %s\nProvider: %s\nModel: %s\nSolver: %s\nTools: %d\nSession: %s", chatID, profileName, provider, model, solver, len(profile.Tools), sessionID)
}

func (g *signalGateway) signalCommandStatus(chatID string) string {
	info, ok := g.gatewayCore().SessionInfo(chatID)
	if !ok {
		return "No active session for this chat."
	}
	status := "idle"
	if info.InFlight {
		status = "busy"
	}
	last := "never"
	if !info.LastStatus.IsZero() {
		last = info.LastStatus.Format(time.RFC3339)
	}
	return fmt.Sprintf("Status: %s\nSession: %s\nRun dir: %s\nLast activity: %s", status, info.SessionID, g.cfg.RunDir, last)
}

func (g *signalGateway) reconfigureSignalSession(ctx context.Context, chatID string, command gatewaycore.Command) error {
	res, err := g.gatewayCore().ReconfigureSession(ctx, chatID, command)
	if err != nil {
		if strings.HasPrefix(err.Error(), "usage:") {
			return g.SendText(ctx, chatID, []string{fmt.Sprintf("Usage: /%s <value>", command.Name)})
		}
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Reconfigure failed: %v", err)})
	}
	if res.SessionID == "" && res.Provider == "" && res.Model == "" && res.Solver == "" {
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Usage: /%s <value>", command.Name)})
	}
	return g.SendText(ctx, chatID, []string{fmt.Sprintf("Runtime updated.\nProvider: %s\nModel: %s\nSolver: %s", res.Provider, res.Model, res.Solver)})
}

func (g *signalGateway) switchSignalProfile(ctx context.Context, chatID, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return g.SendText(ctx, chatID, []string{"Usage: /profile <name>"})
	}
	if _, ok := g.cfg.Profiles[profileName]; !ok {
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Unknown profile %q.", profileName)})
	}
	g.cfgMu.Lock()
	if g.cfg.ChatProfiles == nil {
		g.cfg.ChatProfiles = map[string]string{}
	}
	oldProfile, hadOld := g.cfg.ChatProfiles[chatID]
	g.cfg.ChatProfiles[chatID] = profileName
	g.cfgMu.Unlock()
	if _, err := g.gatewayCore().CloseSession(ctx, chatID); err != nil {
		g.restoreSignalChatProfile(chatID, hadOld, oldProfile)
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Profile switch failed: %v", err)})
	}
	if _, err := g.gatewayCore().GetOrCreateSession(ctx, chatID); err != nil {
		g.restoreSignalChatProfile(chatID, hadOld, oldProfile)
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Profile switch failed: %v", err)})
	}
	if g.state != nil {
		if err := g.state.ClearChat(chatID); err != nil {
			return g.SendText(ctx, chatID, []string{fmt.Sprintf("Profile changed, but Signal state cleanup failed: %v", err)})
		}
		if state, ok := g.gatewayCore().SessionInfo(chatID); ok {
			if err := g.state.SetBinding(chatID, state.RunID, state.SessionID, time.Now()); err != nil {
				return g.SendText(ctx, chatID, []string{fmt.Sprintf("Profile changed, but Signal state binding failed: %v", err)})
			}
		}
	}
	return g.SendText(ctx, chatID, []string{fmt.Sprintf("Profile set to %s. Started a fresh session.", profileName)})
}

func (g *signalGateway) restoreSignalChatProfile(chatID string, hadOld bool, oldProfile string) {
	g.cfgMu.Lock()
	defer g.cfgMu.Unlock()
	if g.cfg.ChatProfiles == nil {
		if hadOld {
			g.cfg.ChatProfiles = map[string]string{}
			g.cfg.ChatProfiles[chatID] = oldProfile
		}
		return
	}
	if hadOld {
		g.cfg.ChatProfiles[chatID] = oldProfile
		return
	}
	delete(g.cfg.ChatProfiles, chatID)
}

func (g *signalGateway) resetSignalSession(ctx context.Context, chatID string) error {
	closed, err := g.gatewayCore().CloseSession(ctx, chatID)
	if err != nil {
		return g.SendText(ctx, chatID, []string{fmt.Sprintf("Reset failed: %v", err)})
	}
	g.clearManualContext(chatID)
	if g.state != nil {
		if err := g.state.ClearChat(chatID); err != nil {
			return g.SendText(ctx, chatID, []string{fmt.Sprintf("Reset failed to clear Signal state: %v", err)})
		}
	}
	if !closed {
		return g.SendText(ctx, chatID, []string{"No active session to reset."})
	}
	return g.SendText(ctx, chatID, []string{"Reset complete. The next message will start a fresh session."})
}

type signalReceiveEnvelope struct {
	Envelope signalEnvelope `json:"envelope"`
}

type signalEnvelope struct {
	Source       string             `json:"source"`
	SourceNumber string             `json:"sourceNumber"`
	SourceName   string             `json:"sourceName"`
	Timestamp    any                `json:"timestamp"`
	DataMessage  *signalDataMessage `json:"dataMessage"`
	SyncMessage  *signalSyncMessage `json:"syncMessage"`
}

type signalDataMessage struct {
	Message     string             `json:"message"`
	Quote       *signalQuote       `json:"quote"`
	Attachments []signalAttachment `json:"attachments"`
}

type signalAttachment struct {
	ID          string `json:"id"`
	ContentType string `json:"contentType"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
}

type signalSyncMessage struct {
	SentMessage *signalSentMessage `json:"sentMessage"`
}

type signalSentMessage struct {
	Destination       string       `json:"destination"`
	DestinationNumber string       `json:"destinationNumber"`
	Timestamp         any          `json:"timestamp"`
	Message           string       `json:"message"`
	Quote             *signalQuote `json:"quote"`
}

type signalQuote struct {
	ID           any    `json:"id"`
	Author       string `json:"author"`
	AuthorNumber string `json:"authorNumber"`
	AuthorUUID   string `json:"authorUuid"`
	Text         string `json:"text"`
}

func newSignalRPC(ctx context.Context, cfg signalRuntimeConfig) (signalRPC, error) {
	switch cfg.RPCMode {
	case "socket", "":
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
		if err != nil {
			return nil, redactSignalAccountError(fmt.Errorf("connect signal-cli socket: %w", err), cfg.Account)
		}
		return &signalJSONRPC{conn: conn, account: cfg.Account}, nil
	case "tcp":
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", cfg.TCP)
		if err != nil {
			return nil, redactSignalAccountError(fmt.Errorf("connect signal-cli tcp: %w", err), cfg.Account)
		}
		return &signalJSONRPC{conn: conn, account: cfg.Account}, nil
	case "stdio":
		cmd := exec.CommandContext(ctx, "signal-cli", "-a", cfg.Account, "jsonRpc", "--receive-mode", "manual")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			_ = stdin.Close()
			_ = stdout.Close()
			return nil, redactSignalAccountError(err, cfg.Account)
		}
		return &signalJSONRPC{conn: stdioReadWriteCloser{Reader: stdout, Writer: stdin, close: func() error {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = cmd.Process.Kill()
			return cmd.Wait()
		}}, account: cfg.Account}, nil
	default:
		return nil, fmt.Errorf("unsupported signal rpc_mode %q", cfg.RPCMode)
	}
}

type stdioReadWriteCloser struct {
	io.Reader
	io.Writer
	close func() error
}

func (c stdioReadWriteCloser) Read(p []byte) (int, error)  { return c.Reader.Read(p) }
func (c stdioReadWriteCloser) Write(p []byte) (int, error) { return c.Writer.Write(p) }
func (c stdioReadWriteCloser) Close() error {
	if c.close == nil {
		return nil
	}
	return c.close()
}

func (c *signalJSONRPC) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *signalJSONRPC) Receive(ctx context.Context) ([]signalReceiveEnvelope, error) {
	var result []signalReceiveEnvelope
	if err := c.Call(ctx, "receive", map[string]any{"timeout": 1, "maxMessages": 100}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func signalTimestampString(timestamp any) string {
	switch v := timestamp.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func signalTimestampInt64(timestamp any) int64 {
	value := signalTimestampString(timestamp)
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func (c *signalJSONRPC) Call(ctx context.Context, method string, params any, out any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("signal rpc client is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := strconv.Itoa(c.nextID)
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if deadline, ok := ctx.Deadline(); ok {
		if conn, ok := c.conn.(interface{ SetDeadline(time.Time) error }); ok {
			_ = conn.SetDeadline(deadline)
		}
	}
	if _, err := c.conn.Write(payload); err != nil {
		return err
	}
	dec := json.NewDecoder(c.conn)
	for {
		var res struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := dec.Decode(&res); err != nil {
			return err
		}
		if res.ID != id {
			continue
		}
		if res.Error != nil {
			return &signalRPCError{Method: method, Code: res.Error.Code, Message: res.Error.Message}
		}
		if out == nil || len(res.Result) == 0 {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	}
}

type signalRPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *signalRPCError) Error() string {
	if e == nil {
		return "signal rpc failed"
	}
	return fmt.Sprintf("signal rpc %s failed: %d: %s", e.Method, e.Code, e.Message)
}

func redactSignalAccountError(err error, account string) error {
	if err == nil || strings.TrimSpace(account) == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), account, "<redacted-signal-account>"))
}

func redactSignalChatID(chatID string) string {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "<empty>"
	}
	runes := []rune(chatID)
	if len(runes) <= 4 {
		return "<redacted>"
	}
	return "<redacted>..." + string(runes[len(runes)-4:])
}
