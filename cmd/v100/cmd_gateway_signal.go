package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	gatewaycore "github.com/tripledoublev/v100/internal/gateway"
	"github.com/tripledoublev/v100/internal/providers"
)

const (
	signalChunkChars            = 3900
	signalBusyMessage           = "Still processing previous request. Please wait for my reply first."
	signalDefaultStatusInterval = 2 * time.Second
	signalPollRetryBase         = 1 * time.Second
	signalPollRetryMax          = 30 * time.Second
	signalShutdownTimeout       = 5 * time.Second
)

type signalRuntimeConfig struct {
	Account         string
	Socket          string
	TCP             string
	RPCMode         string
	RunDir          string
	Workspace       string
	StreamResponses bool
	VoiceReplies    bool
	VoiceReplyMode  string
	StatusInterval  time.Duration
	AllowedNumbers  map[string]struct{}
	Provider        string
	Profile         string
	ChatProfiles    map[string]string
	Profiles        map[string]config.GatewayProfile
	PromptBaseDir   string
}

type signalRPC interface {
	Receive(ctx context.Context) ([]signalReceiveEnvelope, error)
	Call(ctx context.Context, method string, params any, out any) error
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
	rpc       signalRPC
	cli       gatewaycore.ACPClient
	core      *gatewaycore.Core
	globalCfg *config.Config
}

func gatewaySignalCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "signal",
		Short: "Run the Signal gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSignalGateway(cmd.Context(), cfgPath)
		},
	}
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

func setupSignalGateway(ctx context.Context, cfgPath *string) (*signalGateway, func() error, error) {
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return nil, nil, err
	}
	normalized := normalizeSignalConfig(cfg.Signal)
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
	return gw, func() error {
		_ = gw.gatewayCore().CloseAllSessions(context.Background())
		return proc.Stop()
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
	return signalRuntimeConfig{
		Account:         strings.TrimSpace(cfg.Account),
		Socket:          strings.TrimSpace(cfg.Socket),
		TCP:             strings.TrimSpace(cfg.TCP),
		RPCMode:         mode,
		RunDir:          strings.TrimSpace(cfg.RunDir),
		Workspace:       strings.TrimSpace(cfg.Workspace),
		StreamResponses: cfg.StreamResponses,
		VoiceReplies:    cfg.VoiceReplies,
		VoiceReplyMode:  strings.TrimSpace(cfg.VoiceReplyMode),
		StatusInterval:  statusInterval,
		AllowedNumbers:  allowed,
		Provider:        strings.TrimSpace(cfg.Provider),
		Profile:         strings.TrimSpace(cfg.Profile),
		ChatProfiles:    copyStringMap(cfg.ChatProfiles),
	}
}

func (g *signalGateway) Name() string { return "signal" }

func (g *signalGateway) gatewayCore() *gatewaycore.Core {
	if g.core != nil {
		return g.core
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
		PrepareSession: func(chatID string, params *acp.SessionNewParams) error {
			return gatewaycore.ApplyProfileToSessionNew(params, g.effectiveGatewayProfile(chatID), g.cfg.PromptBaseDir)
		},
		VoiceSettings: func(chatID string) gatewaycore.VoiceConfig {
			return gatewayVoiceConfig(g.cfg.VoiceReplies, g.cfg.VoiceReplyMode, g.effectiveGatewayProfile(chatID))
		},
	}, g.cli)
	return g.core
}

func (g *signalGateway) Poll(ctx context.Context) ([]gatewaycore.Update, error) {
	envelopes, err := g.rpc.Receive(ctx)
	if err != nil {
		return nil, err
	}
	updates := make([]gatewaycore.Update, 0, len(envelopes))
	for _, env := range envelopes {
		number := strings.TrimSpace(env.Envelope.Source)
		if number == "" {
			number = strings.TrimSpace(env.Envelope.SourceNumber)
		}
		if number == "" || !g.Allowed(number) {
			continue
		}
		msg := ""
		if env.Envelope.DataMessage != nil {
			msg = strings.TrimSpace(env.Envelope.DataMessage.Message)
		}
		if msg == "" {
			continue
		}
		if command, ok := gatewaycore.ParseCommand(msg); ok {
			if err := g.handleSignalCommand(ctx, number, command); err != nil {
				return nil, err
			}
			continue
		}
		msgID := signalTimestampString(env.Envelope.Timestamp)
		go func(cID, mID, text string) {
			if emoji := g.chooseReaction(ctx, cID, text); emoji != "" {
				_ = g.React(ctx, cID, mID, emoji)
			}
		}(number, msgID, msg)
		updates = append(updates, gatewaycore.Update{
			ChatID:    number,
			MessageID: msgID,
			Text:      msg,
		})
	}
	return updates, nil
}

func (g *signalGateway) SendText(ctx context.Context, chatID string, chunks []string) error {
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if err := g.rpc.Call(ctx, "send", map[string]any{
			"account":   g.cfg.Account,
			"recipient": chatID,
			"message":   chunk,
		}, nil); err != nil {
			return err
		}
	}
	return nil
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

func (g *signalGateway) Allowed(chatID string) bool {
	if len(g.cfg.AllowedNumbers) == 0 {
		return true
	}
	_, ok := g.cfg.AllowedNumbers[strings.TrimSpace(chatID)]
	return ok
}

func (g *signalGateway) effectiveGatewayProfile(chatID string) gatewaycore.ProfileRuntime {
	return gatewaycore.ResolveProfile(g.cfg.Profiles, g.cfg.Profile, g.cfg.ChatProfiles, chatID)
}

func (g *signalGateway) handleSignalCommand(ctx context.Context, number string, command gatewaycore.Command) error {
	switch command.Name {
	case "help":
		return g.SendText(ctx, number, []string{"Available commands:\n/help\n/reset"})
	case "reset":
		_, err := g.gatewayCore().CloseSession(ctx, number)
		return err
	default:
		return g.SendText(ctx, number, []string{fmt.Sprintf("Unknown command /%s. Try /help.", command.Name)})
	}
}

type signalReceiveEnvelope struct {
	Envelope signalEnvelope `json:"envelope"`
}

type signalEnvelope struct {
	Source       string             `json:"source"`
	SourceNumber string             `json:"sourceNumber"`
	Timestamp    any                `json:"timestamp"`
	DataMessage  *signalDataMessage `json:"dataMessage"`
}

type signalDataMessage struct {
	Message string `json:"message"`
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
		cmd := exec.CommandContext(ctx, "signal-cli", "-a", cfg.Account, "jsonRpc")
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
	scanner := bufio.NewScanner(c.conn)
	for scanner.Scan() {
		var res struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &res); err != nil {
			continue
		}
		if res.ID != id {
			continue
		}
		if res.Error != nil {
			return fmt.Errorf("signal rpc %s failed: %d: %s", method, res.Error.Code, res.Error.Message)
		}
		if out == nil || len(res.Result) == 0 {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

func redactSignalAccountError(err error, account string) error {
	if err == nil || strings.TrimSpace(account) == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), account, "<redacted-signal-account>"))
}
