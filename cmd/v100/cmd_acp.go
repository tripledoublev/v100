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
	conn     *acp.Conn
	yolo     bool
	sessions map[string]*acpSession
	mu       sync.Mutex
	cfgPath  string
	cmd      *cobra.Command
}

type acpSession struct {
	comp         *RunComponents
	loop         *core.Loop
	cancel       context.CancelFunc
	activeCtx    context.Context
	promptActive bool
	mu           sync.Mutex
	closing      bool
}

func (s *acpServer) serve() error {
	for {
		msg, err := s.conn.ReadMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var req acp.Request
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = s.conn.SendError(nil, acp.ErrParse, "parse error")
			continue
		}

		if req.ID != nil {
			go s.handleRequest(req)
		} else {
			go s.handleNotification(req)
		}
	}
}

func (s *acpServer) handleRequest(req acp.Request) {
	switch req.Method {
	case "initialize":
		res := acp.InitializeResult{
			ProtocolVersion: 1,
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

		_ = s.conn.SendResponse(req.ID, res)

	case "session/new":
		var params acp.SessionNewParams
		_ = json.Unmarshal(req.Params, &params)

		cfg, err := loadConfig(s.cfgPath)
		if err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInternal, err.Error())
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
			_ = s.conn.SendError(req.ID, acp.ErrInternal, err.Error())
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
		s.sessions[sessionID] = &acpSession{
			comp: comp,
			loop: loop,
		}
		s.mu.Unlock()

		_ = s.conn.SendResponse(req.ID, acp.SessionNewResult{SessionID: sessionID})

	case "session/prompt":
		var params acp.SessionPromptParams
		_ = json.Unmarshal(req.Params, &params)

		s.mu.Lock()
		session, ok := s.sessions[params.SessionID]
		s.mu.Unlock()

		if !ok {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, "session not found")
			return
		}

		session.mu.Lock()
		if session.closing {
			session.mu.Unlock()
			_ = s.conn.SendError(req.ID, acp.ErrInternal, "session is closing")
			return
		}
		if session.promptActive {
			session.mu.Unlock()
			_ = s.conn.SendError(req.ID, acp.ErrInternal, "a prompt turn is already active for this session")
			return
		}
		session.promptActive = true
		session.mu.Unlock()

		defer func() {
			session.mu.Lock()
			session.promptActive = false
			closing := session.closing
			session.mu.Unlock()

			if closing {
				session.cleanup()
			}
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

		stopReason := s.runPrompt(session, promptText, images)
		_ = s.conn.SendResponse(req.ID, acp.SessionPromptResult{StopReason: stopReason})

	case "session/close":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(req.Params, &params)

		s.mu.Lock()
		session, ok := s.sessions[params.SessionID]
		if ok {
			delete(s.sessions, params.SessionID)
		}
		s.mu.Unlock()

		if ok {
			session.mu.Lock()
			session.closing = true
			if session.cancel != nil {
				session.cancel()
			}
			active := session.promptActive
			session.mu.Unlock()

			if !active {
				session.cleanup()
			}
		}
		_ = s.conn.SendResponse(req.ID, nil)

	default:
		_ = s.conn.SendError(req.ID, acp.ErrMethodNotFound, "method not found")
	}
}

func (s *acpServer) handleNotification(req acp.Request) {
	switch req.Method {
	case "session/cancel":
		var params acp.SessionCancelParams
		_ = json.Unmarshal(req.Params, &params)

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

func (s *acpServer) runPrompt(session *acpSession, prompt string, images []providers.ImageAttachment) string {
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

	ctxMeta, cancelMeta := context.WithTimeout(ctx, 5*time.Second)
	defer cancelMeta()
	metadata, _ := session.loop.Provider.Metadata(ctxMeta, session.comp.Model)
	session.loop.ModelMetadata = metadata

	var err error
	if len(images) > 0 {
		err = session.loop.StepWithImages(ctx, prompt, images)
	} else {
		err = session.loop.Step(ctx, prompt)
	}

	if err != nil {
		if ctx.Err() == context.Canceled {
			return "cancelled"
		}
		return "error"
	}

	return "end_turn"
}

func (s *acpSession) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.comp != nil {
		if s.comp.Trace != nil {
			_ = s.comp.Trace.Close()
		}
		if s.comp.Config != nil && s.comp.Config.Sandbox.Enabled && s.comp.Session != nil {
			_ = s.comp.Session.Close()
		}
	}
}
