package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/tripledoublev/v100/internal/acp"
	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/core/executor"
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
	closeReason  string
	runStarted   bool
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
				LoadSession: true,
				SessionCapabilities: acp.SessionCapabilities{
					Close:  &struct{}{},
					List:   &struct{}{},
					Resume: &struct{}{},
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

	case acp.MethodSessionList:
		var params acp.SessionListParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		res, err := s.listSessions(params)
		if err != nil {
			_ = s.conn.SendError(req.ID, acpErrorCode(err), err.Error())
			return
		}
		_ = s.conn.SendResponse(req.ID, res)

	case acp.MethodFinalize:
		var params acp.FinalizeParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		closed := s.finalize(params.Reason)
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
		registerAgentTool(cfg, comp.Registry, comp.Trace, comp.Budget, &outputFn, confirmFn, comp.Workspace, cfg.Defaults.MaxToolCallsPerStep, comp.Session, comp.Mapper, comp.ToolEnv, comp.RedactToolOutput)

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
			ToolEnv:          append([]string(nil), comp.ToolEnv...),
			RedactToolOutput: comp.RedactToolOutput,
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

		s.sendAvailableCommands(sessionID)

	case acp.MethodSessionResume, acp.MethodSessionLoad:
		var params acp.SessionResumeParams
		if err := decodeACPParams(req.Params, &params); err != nil {
			_ = s.conn.SendError(req.ID, acp.ErrInvalidParams, err.Error())
			return
		}
		res, err := s.resumeSession(params)
		if err != nil {
			_ = s.conn.SendError(req.ID, acpErrorCode(err), err.Error())
			return
		}
		_ = s.conn.SendResponse(req.ID, res)
		s.sendAvailableCommands(res.SessionID)

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
		closeACPSession(session, "session_close")
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

func acpStatus(code int, format string, args ...any) error {
	return acpStatusError{code: code, msg: fmt.Sprintf(format, args...)}
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

func (s *acpServer) listSessions(params acp.SessionListParams) (acp.SessionListResult, error) {
	runRoot := strings.TrimSpace(params.RunDir)
	if runRoot == "" {
		runRoot = "runs"
	}
	runRoot = expandHomePath(runRoot)
	if abs, err := filepath.Abs(runRoot); err == nil {
		runRoot = abs
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	s.mu.Lock()
	activeSessions := make(map[string]*acpSession, len(s.sessions))
	for id, session := range s.sessions {
		activeSessions[id] = session
	}
	s.mu.Unlock()

	activeRunIDs := make(map[string]bool, len(activeSessions))
	infos := make([]acp.SessionInfo, 0, len(activeSessions))
	for sessionID, session := range activeSessions {
		info := acpActiveSessionInfo(sessionID, session)
		if info.RunID != "" {
			activeRunIDs[info.RunID] = true
		}
		if info.SessionID != "" {
			activeRunIDs[info.SessionID] = true
		}
		infos = append(infos, info)
	}

	restorable, err := acpRestorableRunInfos(runRoot, activeRunIDs)
	if err != nil && !os.IsNotExist(err) {
		return acp.SessionListResult{}, err
	}
	infos = append(infos, restorable...)

	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].LastUpdate == infos[j].LastUpdate {
			return infos[i].RunID > infos[j].RunID
		}
		return infos[i].LastUpdate > infos[j].LastUpdate
	})
	if limit > 0 && len(infos) > limit {
		infos = infos[:limit]
	}
	return acp.SessionListResult{Sessions: infos}, nil
}

func acpActiveSessionInfo(sessionID string, session *acpSession) acp.SessionInfo {
	info := acp.SessionInfo{
		SessionID:  sessionID,
		RunID:      sessionID,
		State:      "active",
		Active:     true,
		Restorable: true,
	}
	if session == nil {
		return info
	}

	session.mu.Lock()
	closing := session.closing
	promptActive := session.promptActive
	comp := session.comp
	loop := session.loop
	session.mu.Unlock()

	switch {
	case closing:
		info.State = "closing"
	case promptActive:
		info.State = "busy"
	}
	if comp != nil {
		if comp.Run != nil {
			info.RunID = strings.TrimSpace(comp.Run.ID)
			info.TracePath = strings.TrimSpace(comp.Run.TraceFile)
			if info.TracePath != "" {
				info.RunDir = filepath.Dir(info.TracePath)
			}
		}
		if comp.Provider != nil {
			info.Provider = strings.TrimSpace(comp.Provider.Name())
		}
		info.Model = strings.TrimSpace(comp.Model)
		info.Workspace = strings.TrimSpace(comp.Workspace)
	}
	if loop != nil {
		if info.Model == "" {
			info.Model = strings.TrimSpace(loop.Model)
		}
		if info.Provider == "" && loop.Provider != nil {
			info.Provider = strings.TrimSpace(loop.Provider.Name())
		}
		if info.TracePath == "" && loop.Run != nil {
			info.TracePath = strings.TrimSpace(loop.Run.TraceFile)
			if info.RunID == "" {
				info.RunID = strings.TrimSpace(loop.Run.ID)
			}
			if info.TracePath != "" {
				info.RunDir = filepath.Dir(info.TracePath)
			}
		}
	}
	if info.RunID == "" {
		info.RunID = sessionID
	}
	if info.RunDir != "" {
		enrichACPSessionInfo(&info, info.RunDir)
	}
	if info.TracePath != "" {
		info.LastUpdate = acpTraceModTime(info.TracePath)
	}
	return info
}

func acpRestorableRunInfos(runRoot string, activeRunIDs map[string]bool) ([]acp.SessionInfo, error) {
	entries, err := os.ReadDir(runRoot)
	if err != nil {
		return nil, err
	}
	infos := make([]acp.SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		if activeRunIDs[runID] {
			continue
		}
		runDir := filepath.Join(runRoot, runID)
		tracePath := filepath.Join(runDir, "trace.jsonl")
		if _, err := os.Stat(tracePath); err != nil {
			continue
		}
		info := acp.SessionInfo{
			SessionID:  runID,
			RunID:      runID,
			RunDir:     runDir,
			TracePath:  tracePath,
			State:      "restorable",
			Restorable: true,
		}
		enrichACPSessionInfo(&info, runDir)
		if activeRunIDs[info.RunID] || activeRunIDs[info.SessionID] {
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func enrichACPSessionInfo(info *acp.SessionInfo, runDir string) {
	if info == nil {
		return
	}
	if strings.TrimSpace(runDir) == "" {
		return
	}
	if meta, err := core.ReadMeta(runDir); err == nil {
		if strings.TrimSpace(meta.RunID) != "" {
			info.RunID = strings.TrimSpace(meta.RunID)
			if strings.TrimSpace(info.SessionID) == "" {
				info.SessionID = info.RunID
			}
		}
		if info.Provider == "" {
			info.Provider = strings.TrimSpace(meta.Provider)
		}
		if info.Model == "" {
			info.Model = strings.TrimSpace(meta.Model)
		}
		if info.Workspace == "" {
			info.Workspace = strings.TrimSpace(meta.SourceWorkspace)
		}
	}

	tracePath := filepath.Join(runDir, "trace.jsonl")
	info.TracePath = tracePath
	info.RunDir = runDir
	info.LastUpdate = acpTraceModTime(tracePath)
	if events, err := core.ReadAll(tracePath); err == nil {
		provider, model, workspace, endReason := acpTraceSummary(events)
		if provider != "" {
			info.Provider = provider
		}
		if model != "" {
			info.Model = model
		}
		if workspace != "" && info.Workspace == "" {
			info.Workspace = workspace
		}
		if endReason != "" {
			info.EndReason = endReason
			if !info.Active {
				info.State = "ended"
			}
		}
	}
	if info.SessionID == "" {
		info.SessionID = info.RunID
	}
}

func acpTraceSummary(events []core.Event) (provider, model, workspace, endReason string) {
	for _, ev := range events {
		switch ev.Type {
		case core.EventRunStart:
			var payload core.RunStartPayload
			_ = json.Unmarshal(ev.Payload, &payload)
			if strings.TrimSpace(payload.Provider) != "" {
				provider = strings.TrimSpace(payload.Provider)
			}
			if strings.TrimSpace(payload.Model) != "" {
				model = strings.TrimSpace(payload.Model)
			}
			if strings.TrimSpace(payload.Workspace) != "" {
				workspace = strings.TrimSpace(payload.Workspace)
			}
		case core.EventRunEnd:
			var payload core.RunEndPayload
			_ = json.Unmarshal(ev.Payload, &payload)
			endReason = strings.TrimSpace(payload.Reason)
		}
	}
	return provider, model, workspace, endReason
}

func acpTraceModTime(tracePath string) string {
	if info, err := os.Stat(tracePath); err == nil {
		return info.ModTime().UTC().Format(time.RFC3339)
	}
	return ""
}

func (s *acpServer) resumeSession(params acp.SessionResumeParams) (acp.SessionResumeResult, error) {
	runDir, runID, err := resolveACPResumeRunDir(params)
	if err != nil {
		return acp.SessionResumeResult{}, err
	}
	tracePath := filepath.Join(runDir, "trace.jsonl")

	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		sessionID = runID
	}
	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		return acp.SessionResumeResult{}, acpStatus(acp.ErrSessionAlreadyExists, "%s: %s", acp.ErrorMessage(acp.ErrSessionAlreadyExists), sessionID)
	}
	s.mu.Unlock()

	events, err := core.ReadAll(tracePath)
	if err != nil {
		return acp.SessionResumeResult{}, fmt.Errorf("read trace: %w", err)
	}
	cfg, err := loadConfig(s.cfgPath)
	if err != nil {
		return acp.SessionResumeResult{}, acpStatus(acp.ErrProviderConfiguration, "%s", err.Error())
	}

	msgs, providerName, model, tracedWorkspace, metadata := reconstructHistory(runDir, events)
	if ckMsgs, err := loadCheckpoint(runDir); err != nil {
		return acp.SessionResumeResult{}, fmt.Errorf("load checkpoint: %w", err)
	} else if len(ckMsgs) > 0 {
		msgs = ckMsgs
	}
	if resumeSummary := buildResumeSummary(runID, events, msgs); strings.TrimSpace(resumeSummary) != "" {
		msgs = append([]providers.Message{{Role: "system", Content: resumeSummary}}, msgs...)
	}

	meta, _ := core.ReadMeta(runDir)
	if meta.Sandbox.Enabled || strings.TrimSpace(meta.Sandbox.Backend) != "" {
		cfg.Sandbox = meta.Sandbox
	}
	providerName, model, selectionChanged := resolveResumeProviderSelection(cfg, providerName, model, "", "")
	if selectionChanged {
		metadata = providers.ModelMetadata{}
	}
	prov, err := buildProviderWithModel(cfg, providerName, model)
	if err != nil {
		return acp.SessionResumeResult{}, acpStatus(acp.ErrProviderConfiguration, "%s", err.Error())
	}
	reg := buildToolRegistry(cfg)
	if err := validateToolRegistry(reg); err != nil {
		return acp.SessionResumeResult{}, acpStatus(acp.ErrProviderConfiguration, "%s", err.Error())
	}
	pol := loadPolicy(cfg, "default")
	if cfg.Defaults.ContextLimit > 0 {
		pol.ContextLimit = cfg.Defaults.ContextLimit
	}
	budget := core.NewBudgetTracker(&core.Budget{
		MaxSteps:   cfg.Defaults.BudgetSteps,
		MaxTokens:  cfg.Defaults.BudgetTokens,
		MaxCostUSD: cfg.Defaults.BudgetCostUSD,
	})
	trace, err := core.OpenTrace(tracePath)
	if err != nil {
		return acp.SessionResumeResult{}, fmt.Errorf("open trace: %w", err)
	}

	run := &core.Run{
		ID:        runID,
		Dir:       runDir,
		TraceFile: tracePath,
		Budget: core.Budget{
			MaxSteps:   cfg.Defaults.BudgetSteps,
			MaxTokens:  cfg.Defaults.BudgetTokens,
			MaxCostUSD: cfg.Defaults.BudgetCostUSD,
		},
	}
	sourceWorkspace := resolveResumeSourceWorkspace(params.CWD, runDir, tracedWorkspace, meta)
	execFactory, err := executor.NewExecutor(cfg.Sandbox, filepath.Dir(runDir))
	if err != nil {
		_ = trace.Close()
		return acp.SessionResumeResult{}, err
	}
	execSession, err := execFactory.NewSession(runID, sourceWorkspace)
	if err != nil {
		_ = trace.Close()
		return acp.SessionResumeResult{}, err
	}

	sandboxWorkspace := sourceWorkspace
	if cfg.Sandbox.Enabled {
		sandboxWorkspace = filepath.Join(filepath.Dir(runDir), runID, "workspace")
		if _, err := os.Stat(sandboxWorkspace); err != nil {
			_ = trace.Close()
			_ = execSession.Close()
			return acp.SessionResumeResult{}, fmt.Errorf("resume sandbox workspace: %w", err)
		}
	}
	mapper := core.NewPathMapper(sourceWorkspace, sandboxWorkspace)
	run.Dir = sandboxWorkspace

	toolEnv, redactToolOutput := buildToolRuntime(cfg)
	confirmMode := cfg.Defaults.ConfirmTools
	if s.yolo {
		confirmMode = "never"
	}
	confirmFn := buildConfirmFn(confirmMode)
	if !s.yolo {
		confirmFn = func(toolName, _ string) bool { return false }
	}
	outputFn := acp.NewTranslator(s.conn, sessionID)
	registerAgentTool(cfg, reg, trace, budget, &outputFn, confirmFn, sandboxWorkspace, cfg.Defaults.MaxToolCallsPerStep, execSession, mapper, toolEnv, redactToolOutput)

	cmd := s.cmd
	if cmd == nil {
		cmd = &cobra.Command{}
	}
	comp := &RunComponents{
		Config:           cfg,
		Run:              run,
		Provider:         prov,
		Registry:         reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		Session:          execSession,
		Mapper:           mapper,
		Workspace:        sandboxWorkspace,
		Model:            model,
		ModelMetadata:    metadata,
		GenParams:        buildGenParams(cfg, 0, 0, 0, 0, 0, cmd),
		ToolEnv:          toolEnv,
		RedactToolOutput: redactToolOutput,
	}
	loop := &core.Loop{
		Run:              run,
		Provider:         prov,
		Model:            model,
		CompressProvider: buildCompressProvider(cfg),
		Tools:            reg,
		Policy:           pol,
		Trace:            trace,
		Budget:           budget,
		Messages:         msgs,
		ConfirmFn:        confirmFn,
		OutputFn:         outputFn,
		Session:          execSession,
		Mapper:           mapper,
		ToolEnv:          append([]string(nil), toolEnv...),
		RedactToolOutput: redactToolOutput,
		ModelMetadata:    metadata,
		NetworkTier:      loopNetworkTier(cfg),
		Snapshots:        buildSnapshotManager(cfg, sandboxWorkspace),
		GenParams:        comp.GenParams,
	}
	loop.Hooks = append(loop.Hooks, core.ThresholdHook(5))
	loop.Hooks = append(loop.Hooks, core.DeduplicationHook(2))

	s.mu.Lock()
	if _, exists := s.sessions[sessionID]; exists {
		s.mu.Unlock()
		cleanupRunComponents(comp)
		return acp.SessionResumeResult{}, acpStatus(acp.ErrSessionAlreadyExists, "%s: %s", acp.ErrorMessage(acp.ErrSessionAlreadyExists), sessionID)
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

	return acp.SessionResumeResult{SessionID: sessionID, RunID: runID}, nil
}

func resolveACPResumeRunDir(params acp.SessionResumeParams) (string, string, error) {
	runDirParam := strings.TrimSpace(params.RunDir)
	target := strings.TrimSpace(params.RunID)
	if target == "" {
		target = strings.TrimSpace(params.SessionID)
	}
	if runDirParam != "" {
		runDirParam = expandHomePath(runDirParam)
		if hasACPTrace(runDirParam) {
			runID := target
			if runID == "" {
				runID = filepath.Base(filepath.Clean(runDirParam))
			}
			return runDirParam, runID, nil
		}
		if target == "" {
			return "", "", acpStatus(acp.ErrInvalidParams, "session resume requires runId when runDir is a runs root")
		}
		candidate := filepath.Join(runDirParam, target)
		if hasACPTrace(candidate) {
			return candidate, target, nil
		}
		return "", "", acpStatus(acp.ErrSessionNotFound, "%s: %s", acp.ErrorMessage(acp.ErrSessionNotFound), target)
	}
	if target == "" {
		return "", "", acpStatus(acp.ErrInvalidParams, "session resume requires runId, runDir, or sessionId")
	}
	runDir, err := findRunDir(target)
	if err != nil {
		return "", "", acpStatus(acp.ErrSessionNotFound, "%s: %s", acp.ErrorMessage(acp.ErrSessionNotFound), target)
	}
	return runDir, filepath.Base(filepath.Clean(runDir)), nil
}

func hasACPTrace(runDir string) bool {
	if strings.TrimSpace(runDir) == "" {
		return false
	}
	if info, err := os.Stat(filepath.Join(runDir, "trace.jsonl")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func (s *acpServer) finalize(reason string) int {
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
		closeACPSession(session, acpCloseReason(reason, "finalize"))
		closed++
	}
	return closed
}

func closeACPSession(session *acpSession, reason string) {
	if session == nil {
		return
	}
	cleanupDone := session.cleanupDoneChan()
	session.mu.Lock()
	session.closing = true
	session.closeReason = acpCloseReason(reason, "session_close")
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

func (s *acpServer) sendAvailableCommands(sessionID string) {
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

	if err := session.emitRunStart(); err != nil {
		_ = s.conn.SendNotification(acp.MethodSessionUpdate, acp.SessionUpdateParams{
			SessionID: sessionID,
			Update: acp.Update{
				Type: "agent_message_chunk",
				Content: &acp.ContentBlock{
					Type: "text",
					Text: "Error: failed to start run trace: " + err.Error(),
				},
			},
		})
		return "refusal"
	}

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
		loop := s.loop
		runStarted := s.runStarted
		closeReason := s.closeReason
		s.comp = nil
		s.loop = nil
		s.mu.Unlock()
		if runStarted && loop != nil {
			_ = loop.EmitRunEnd(acpCloseReason(closeReason, "session_close"), "")
		}
		cleanupRunComponents(comp)
		close(cleanupDone)
	})
}

func (s *acpSession) emitRunStart() error {
	s.mu.Lock()
	if s.runStarted {
		s.mu.Unlock()
		return nil
	}
	loop := s.loop
	comp := s.comp
	s.runStarted = true
	s.mu.Unlock()

	if loop == nil {
		return nil
	}
	if err := loop.EmitRunStart(acpRunStartPayload(loop, comp)); err != nil {
		s.mu.Lock()
		s.runStarted = false
		s.mu.Unlock()
		return err
	}
	return nil
}

func acpRunStartPayload(loop *core.Loop, comp *RunComponents) core.RunStartPayload {
	var (
		cfg          *config.Config
		policyName   string
		providerName string
		model        string
		workspace    string
		metadata     providers.ModelMetadata
	)
	if loop != nil {
		if loop.Policy != nil {
			policyName = loop.Policy.Name
		}
		if loop.Provider != nil {
			providerName = loop.Provider.Name()
		}
		model = loop.Model
		metadata = loop.ModelMetadata
		if loop.Run != nil {
			workspace = loop.Run.Dir
		}
	}
	if comp != nil {
		cfg = comp.Config
		if providerName == "" && comp.Provider != nil {
			providerName = comp.Provider.Name()
		}
		if model == "" {
			model = comp.Model
		}
		if workspace == "" {
			workspace = comp.Workspace
		}
		if metadata == (providers.ModelMetadata{}) {
			metadata = comp.ModelMetadata
		}
	}
	return core.RunStartPayload{
		Policy:        policyName,
		Provider:      providerName,
		Model:         model,
		Workspace:     traceWorkspace(cfg, workspace),
		ModelMetadata: metadata,
	}
}

func acpCloseReason(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason != "" {
		return reason
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return "session_close"
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
