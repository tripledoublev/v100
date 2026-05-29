package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/providers"
)

func acpCmd(cfgPath *string) *cobra.Command {
	var (
		yoloFlag bool
		autoFlag bool
	)

	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Start v100 in Agent Client Protocol (ACP) mode",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. IO Isolation
			realStdout := os.Stdout
			os.Stdout = os.Stderr

			conn := acp.NewConn(os.Stdin, realStdout)
			server := &acpServer{
				conn:     conn,
				yolo:     yoloFlag || autoFlag,
				sessions: make(map[string]*acpSession),
				cfgPath:  *cfgPath,
				cmd:      cmd,
			}

			return server.serve()
		},
	}

	cmd.Flags().BoolVar(&yoloFlag, "yolo", false, "auto-approve all tool calls")
	cmd.Flags().BoolVar(&autoFlag, "auto", false, "auto-approve all tool calls")

	return cmd
}

type acpServer struct {
	conn             *acp.Conn
	yolo             bool
	sessions         map[string]*acpSession
	suggestedPrompts []acp.SuggestedPrompt
	initialized      bool
	clientInfo       acp.ClientInfo
	clientCaps       acp.ClientCapabilities
	shutdown         chan struct{}
	shutdownOnce     sync.Once
	mu               sync.Mutex
	cfgPath          string
	cmd              *cobra.Command
}

type acpSession struct {
	comp         *RunComponents
	loop         *core.Loop
	cancel       context.CancelFunc
	activeCtx    context.Context
	promptActive bool
	mu           sync.Mutex
	closing      bool
	prompts      []acp.SuggestedPrompt
	cleanupDone  chan struct{}
	cleanupOnce  sync.Once
}

func (s *acpServer) serve() error {
	shutdown := s.shutdownChan()
	messages := make(chan []byte)
	readErrs := make(chan error, 1)

	go func() {
		for {
			msg, err := s.conn.ReadMessage()
			if err != nil {
				select {
				case readErrs <- err:
				case <-shutdown:
				}
				return
			}
			msg = append([]byte(nil), msg...)
			select {
			case messages <- msg:
			case <-shutdown:
				return
			}
		}
	}()

	for {
		select {
		case <-shutdown:
			return nil
		case err := <-readErrs:
			if err == io.EOF {
				return nil
			}
			return err
		case msg := <-messages:
			var req acp.Request
			if err := json.Unmarshal(msg, &req); err != nil {
				_ = s.conn.SendError(nil, acp.ErrParse, acp.ErrorMessage(acp.ErrParse))
				continue
			}

			if req.ID != nil {
				go s.handleRequest(req)
			} else {
				go s.handleNotification(req)
			}
		}
	}
}

func (s *acpServer) handleRequest(req acp.Request) {
	if req.Method != acp.MethodFinalize && s.isShuttingDown() {
		_ = s.conn.SendError(req.ID, acp.ErrInvalidRequest, "ACP server has finalized")
		return
	}

	switch req.Method {
	case acp.MethodInitialize:
		var params acp.InitializeParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		res := acp.InitializeResult{
			ProtocolVersion: acp.ProtocolVersion,
			AgentCapabilities: acp.AgentCapabilities{
				SessionCapabilities: acp.SessionCapabilities{
					Close: &struct{}{},
				},
			},
			AgentInfo: acp.AgentInfo{
				Name:    "v100",
				Version: version,
			},
		}
		cfg, _ := loadConfig(s.cfgPath)
		if cfg != nil {
			if prov, err := buildProvider(cfg, cfg.Defaults.Provider); err == nil {
				res.AgentCapabilities.PromptCapabilities.Image = prov.Capabilities().Images
			}
		}
		s.mu.Lock()
		s.initialized = true
		s.clientInfo = params.ClientInfo
		s.clientCaps = params.ClientCapabilities
		s.mu.Unlock()

		_ = s.conn.SendResponse(req.ID, res)

	case acp.MethodFinalize:
		var params acp.FinalizeParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		closed := s.finalize()
		_ = s.conn.SendResponse(req.ID, acp.FinalizeResult{ClosedSessions: closed})
		s.signalShutdown()

	case acp.MethodSetSuggestedPrompts:
		var params acp.SetSuggestedPromptsParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		if err := s.setSuggestedPrompts(params); err != nil {
			_ = s.conn.SendError(req.ID, acpErrorCode(err), err.Error())
			return
		}
		_ = s.conn.SendResponse(req.ID, acp.SetSuggestedPromptsResult{Count: len(params.Prompts)})

	case acp.MethodSessionNew:
		var params acp.SessionNewParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}

		cfg, err := loadConfig(s.cfgPath)
		if err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrProviderConfiguration, err.Error())
			return
		}

		if _, ok := cfg.Providers[cfg.Defaults.Provider]; !ok {
			defaults := config.DefaultConfig()
			if pc, ok := defaults.Providers[cfg.Defaults.Provider]; ok {
				cfg.Providers[cfg.Defaults.Provider] = pc
			}
		}

		opts := RunOptions{
			Workspace: params.CWD,
		}

		comp, err := BuildRunComponents(cfg, opts)
		if err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrProviderConfiguration, err.Error())
			return
		}

		comp.GenParams = buildGenParams(cfg, 0, 0, 0, 0, 0, s.cmd)

		comp.Run.Dir = comp.Workspace

		sessionID := params.SessionID
		if sessionID == "" {
			sessionID = comp.Run.ID
		}

		confirmMode := cfg.Defaults.ConfirmTools
		if s.yolo {
			confirmMode = "never"
		}
		confirmFn := buildConfirmFn(confirmMode)
		if !s.yolo {
			confirmFn = func(toolName, _ string) bool { return false }
		}

		outputFn := acp.NewTranslator(s.conn, sessionID)
		registerAgentTool(cfg, comp.Registry, comp.Trace, comp.Budget, &outputFn, confirmFn, comp.Workspace, cfg.Defaults.MaxToolCallsPerStep, comp.Session, comp.Mapper)

		loop := &core.Loop{
			Run:              comp.Run,
			Provider:         comp.Provider,
			Model:            comp.Model,
			EmbedProvider:    comp.EmbedProvider,
			CompressProvider: buildCompressProvider(cfg),
			Tools:            comp.Registry,
			Policy:           comp.Policy,
			Trace:            comp.Trace,
			Budget:           comp.Budget,
			ConfirmFn:        confirmFn,
			OutputFn:         outputFn,
			Session:          comp.Session,
			Mapper:           comp.Mapper,
			NetworkTier:      loopNetworkTier(cfg),
			Snapshots:        buildSnapshotManager(cfg, comp.Workspace),
			Solver:           comp.Solver,
			GenParams:        comp.GenParams,
		}

		loop.Hooks = append(loop.Hooks, core.ThresholdHook(5))
		loop.Hooks = append(loop.Hooks, core.DeduplicationHook(2))

		s.mu.Lock()
		if _, exists := s.sessions[sessionID]; exists {
			s.mu.Unlock()
			cleanupRunComponents(comp)
			_ = s.conn.SendError(req.ID, acp.ErrSessionAlreadyExists, fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionAlreadyExists), sessionID))
			return
		}
		if s.sessions == nil {
			s.sessions = make(map[string]*acpSession)
		}
		s.sessions[sessionID] = &acpSession{
			comp:    comp,
			loop:    loop,
			prompts: copySuggestedPrompts(s.suggestedPrompts),
		}
		s.mu.Unlock()

		_ = s.conn.SendResponse(req.ID, acp.SessionNewResult{SessionID: sessionID})

		// Advertise available slash commands to the client
		_ = s.conn.SendNotification(acp.MethodSessionUpdate, acp.SessionUpdateParams{
			SessionID: sessionID,
			Update: acp.Update{
				Type: "available_commands_update",
				AvailableCommands: []acp.Command{
					{Name: "model", Description: "switch model or provider"},
					{Name: "auto", Description: "switch to auto (smartrouter) mode"},
					{Name: "local", Description: "switch to local provider"},
				},
			},
		})

	case acp.MethodSessionPrompt:
		var params acp.SessionPromptParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}

		s.mu.Lock()
		session, ok := s.sessions[params.SessionID]
		s.mu.Unlock()

		if !ok {
			_ = s.conn.SendError(req.ID, acp.ErrSessionNotFound, fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionNotFound), params.SessionID))
			return
		}

		session.mu.Lock()
		if session.closing {
			session.mu.Unlock()
			_ = s.conn.SendError(req.ID, acp.ErrSessionClosing, fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionClosing), params.SessionID))
			return
		}
		if session.promptActive {
			session.mu.Unlock()
			_ = s.conn.SendError(req.ID, acp.ErrSessionBusy, fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionBusy), params.SessionID))
			return
		}
		session.promptActive = true
		session.mu.Unlock()

		defer func() {
			session.finishPrompt()
		}()

		var promptText string
		var images []providers.ImageAttachment
		for _, b := range params.Prompt {
			switch b.Type {
			case "text":
				if promptText != "" {
					promptText += "\n"
				}
				promptText += b.Text
			case "image":
				raw, err := base64.StdEncoding.DecodeString(b.Data)
				if err != nil {
					_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, "invalid image data: "+err.Error())
					return
				}
				images = append(images, providers.ImageAttachment{
					MIMEType: b.MimeType,
					Data:     raw,
				})
			case "resource_link":
				if promptText != "" {
					promptText += "\n"
				}
				promptText += fmt.Sprintf("[resource: %s (%s)]", b.Name, b.URI)
			}
		}

		stopReason := s.runPrompt(session, params.SessionID, promptText, images)
		_ = s.conn.SendResponse(req.ID, acp.SessionPromptResult{StopReason: stopReason})

	case acp.MethodSessionClose:
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}

		s.mu.Lock()
		session, ok := s.sessions[params.SessionID]
		if ok {
			delete(s.sessions, params.SessionID)
		}
		s.mu.Unlock()

		if !ok {
			_ = s.conn.SendError(req.ID, acp.ErrSessionNotFound, fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionNotFound), params.SessionID))
			return
		}
		closeACPSession(session)
		_ = s.conn.SendResponse(req.ID, nil)

	default:
		_ = s.conn.SendError(req.ID, acp.ErrMethodNotFound, acp.ErrorMessage(acp.ErrMethodNotFound))
	}
}

func (s *acpServer) handleNotification(req acp.Request) {
	switch req.Method {
	case acp.MethodSessionCancel:
		var params acp.SessionCancelParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			return
		}

		s.mu.Lock()
		session, ok := s.sessions[params.SessionID]
		s.mu.Unlock()

		if ok {
			session.mu.Lock()
			if session.cancel != nil {
				session.cancel()
			}
			session.mu.Unlock()
		}
	}
}

func decodeACPParams(raw json.RawMessage, dest any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dest)
}

func (s *acpServer) shutdownChan() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown == nil {
		s.shutdown = make(chan struct{})
	}
	return s.shutdown
}

func (s *acpServer) signalShutdown() {
	ch := s.shutdownChan()
	s.shutdownOnce.Do(func() {
		close(ch)
	})
}

func (s *acpServer) isShuttingDown() bool {
	ch := s.shutdownChan()
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

type acpStatusError struct {
	code int
	msg  string
}

func (e acpStatusError) Error() string { return e.msg }

func acpErrorCode(err error) int {
	if e, ok := err.(acpStatusError); ok {
		return e.code
	}
	return acp.ErrInternal
}

func (s *acpServer) setSuggestedPrompts(params acp.SetSuggestedPromptsParams) error {
	prompts := copySuggestedPrompts(params.Prompts)
	if params.SessionID == "" {
		s.mu.Lock()
		s.suggestedPrompts = prompts
		s.mu.Unlock()
		return nil
	}

	s.mu.Lock()
	session, ok := s.sessions[params.SessionID]
	s.mu.Unlock()
	if !ok {
		return acpStatusError{
			code: acp.ErrSessionNotFound,
			msg:  fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionNotFound), params.SessionID),
		}
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closing {
		return acpStatusError{
			code: acp.ErrSessionClosing,
			msg:  fmt.Sprintf("%s: %s", acp.ErrorMessage(acp.ErrSessionClosing), params.SessionID),
		}
	}
	session.prompts = prompts
	return nil
}

func (s *acpServer) finalize() int {
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = make(map[string]*acpSession)
	s.suggestedPrompts = nil
	s.initialized = false
	s.clientInfo = acp.ClientInfo{}
	s.clientCaps = acp.ClientCapabilities{}
	s.mu.Unlock()

	closed := 0
	for _, session := range sessions {
		closeACPSession(session)
		closed++
	}
	return closed
}

func closeACPSession(session *acpSession) {
	if session == nil {
		return
	}
	cleanupDone := session.cleanupDoneChan()
	session.mu.Lock()
	session.closing = true
	if session.cancel != nil {
		session.cancel()
	}
	active := session.promptActive
	session.mu.Unlock()
	if !active {
		session.cleanup()
		return
	}

	select {
	case <-cleanupDone:
	case <-time.After(5 * time.Second):
	}
}

func copySuggestedPrompts(in []acp.SuggestedPrompt) []acp.SuggestedPrompt {
	if len(in) == 0 {
		return nil
	}
	out := make([]acp.SuggestedPrompt, len(in))
	copy(out, in)
	for i := range out {
		out[i].Tags = append([]string(nil), in[i].Tags...)
	}
	return out
}

func (s *acpServer) runPrompt(session *acpSession, sessionID string, prompt string, images []providers.ImageAttachment) string {
	ctx, cancel := context.WithCancel(context.Background())

	session.mu.Lock()
	session.cancel = cancel
	session.activeCtx = ctx
	session.mu.Unlock()

	defer func() {
		session.mu.Lock()
		session.cancel = nil
		session.activeCtx = nil
		session.mu.Unlock()
	}()

	cfg, err := loadConfig(s.cfgPath)
	if err != nil {
		_ = s.conn.SendNotification(acp.MethodSessionUpdate, acp.SessionUpdateParams{
			SessionID: sessionID,
			Update: acp.Update{
				Type: "agent_message_chunk",
				Content: &acp.ContentBlock{
					Type: "text",
					Text: "Error: failed to load config: " + err.Error(),
				},
			},
		})
		return "refusal"
	}
	rewritten, handled, err := applyInteractiveMode(ctx, cfg, session.loop, prompt, false)
	if err != nil {
		_ = s.conn.SendNotification(acp.MethodSessionUpdate, acp.SessionUpdateParams{
			SessionID: sessionID,
			Update: acp.Update{
				Type: "agent_message_chunk",
				Content: &acp.ContentBlock{
					Type: "text",
					Text: "Error: " + err.Error(),
				},
			},
		})
		return "refusal"
	}
	if handled {
		return "end_turn"
	}
	prompt = rewritten

	ctxMeta, cancelMeta := context.WithTimeout(ctx, 5*time.Second)
	defer cancelMeta()
	metadata, _ := session.loop.Provider.Metadata(ctxMeta, session.loop.Model)
	session.loop.ModelMetadata = metadata

	if len(images) > 0 {
		err = session.loop.StepWithImages(ctx, prompt, images)
	} else {
		err = session.loop.Step(ctx, prompt)
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			return "cancelled"
		}
		return "refusal"
	}

	return "end_turn"
}

func (s *acpSession) cleanup() {
	cleanupDone := s.cleanupDoneChan()
	s.cleanupOnce.Do(func() {
		s.mu.Lock()
		comp := s.comp
		s.comp = nil
		s.loop = nil
		s.mu.Unlock()
		cleanupRunComponents(comp)
		close(cleanupDone)
	})
}

func (s *acpSession) cleanupDoneChan() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleanupDone == nil {
		s.cleanupDone = make(chan struct{})
	}
	return s.cleanupDone
}

func (s *acpSession) finishPrompt() {
	s.mu.Lock()
	s.promptActive = false
	closing := s.closing
	s.mu.Unlock()
	if closing {
		s.cleanup()
	}
}

func cleanupRunComponents(comp *RunComponents) {
	if comp == nil {
		return
	}
	if comp.Trace != nil {
		_ = comp.Trace.Close()
	}
	if comp.Config != nil && comp.Config.Sandbox.Enabled && comp.Session != nil {
		_ = comp.Session.Close()
	}
}
