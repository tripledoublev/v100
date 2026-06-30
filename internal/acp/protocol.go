package acp

import (
	"encoding/json"
)

// JSON-RPC 2.0 Base Structures

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      any             `json:"id"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

const (
	JSONRPCVersion  = "2.0"
	ProtocolVersion = 1
)

const (
	MethodInitialize          = "initialize"
	MethodFinalize            = "finalize"
	MethodSetSuggestedPrompts = "set_suggested_prompts"
	MethodSessionNew          = "session/new"
	MethodSessionList         = "session/list"
	MethodSessionResume       = "session/resume"
	MethodSessionLoad         = "session/load"
	MethodSessionReconfigure  = "session/reconfigure"
	MethodSessionPrompt       = "session/prompt"
	MethodSessionClose        = "session/close"
	MethodSessionCancel       = "session/cancel"
	MethodSessionUpdate       = "session/update"
	// MethodConnectionClosed is a synthetic notification emitted by the client
	// when the underlying transport reaches EOF or errors.
	MethodConnectionClosed = "connection/closed"
)

const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603

	ErrSessionNotFound            = -32001
	ErrSessionAlreadyExists       = -32002
	ErrSessionBusy                = -32003
	ErrSessionClosing             = -32004
	ErrUnsupportedProtocolVersion = -32010
	ErrInvalidSessionConfig       = -32021
	ErrProviderConfiguration      = -32020
)

func ErrorMessage(code int) string {
	switch code {
	case ErrParse:
		return "parse error"
	case ErrInvalidRequest:
		return "invalid request"
	case ErrMethodNotFound:
		return "method not found"
	case ErrInvalidParams:
		return "invalid params"
	case ErrInternal:
		return "internal error"
	case ErrSessionNotFound:
		return "session not found"
	case ErrSessionAlreadyExists:
		return "session already exists"
	case ErrSessionBusy:
		return "session busy"
	case ErrSessionClosing:
		return "session closing"
	case ErrUnsupportedProtocolVersion:
		return "unsupported protocol version"
	case ErrInvalidSessionConfig:
		return "invalid session config"
	case ErrProviderConfiguration:
		return "provider configuration error"
	default:
		return "unknown error"
	}
}

// ContentBlock Structures

type ContentBlock struct {
	Type        string       `json:"type"` // "text", "image", "resource_link"
	Text        string       `json:"text,omitempty"`
	Data        string       `json:"data,omitempty"`     // base64 for image
	MimeType    string       `json:"mimeType,omitempty"` // for image
	URI         string       `json:"uri,omitempty"`      // for resource_link
	Name        string       `json:"name,omitempty"`     // for resource_link
	Annotations *Annotations `json:"annotations,omitempty"`
}

type Annotations struct {
	Confidence float64 `json:"confidence,omitempty"`
}

// ACP Specific Types

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         ClientInfo         `json:"clientInfo"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type ClientCapabilities struct {
	FS       map[string]bool `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         AgentInfo         `json:"agentInfo"`
}

type FinalizeParams struct {
	Reason string `json:"reason,omitempty"`
}

type FinalizeResult struct {
	ClosedSessions int `json:"closedSessions"`
}

type AgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession,omitempty"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities,omitempty"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities,omitempty"`
}

type PromptCapabilities struct {
	Audio           bool `json:"audio,omitempty"`
	Image           bool `json:"image,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

type SessionCapabilities struct {
	Close  *struct{} `json:"close,omitempty"`
	List   *struct{} `json:"list,omitempty"`
	Resume *struct{} `json:"resume,omitempty"`
}

type SessionNewParams struct {
	SessionID     string   `json:"sessionId,omitempty"`
	CWD           string   `json:"cwd,omitempty"`
	RunDir        string   `json:"runDir,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Model         string   `json:"model,omitempty"`
	Solver        string   `json:"solver,omitempty"`
	Tools         []string `json:"tools"`
	Dangerous     []string `json:"dangerous"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	NetworkTier   string   `json:"network_tier,omitempty"`
	BudgetSteps   int      `json:"budget_steps,omitempty"`
	BudgetTokens  int      `json:"budget_tokens,omitempty"`
	BudgetCostUSD float64  `json:"budget_cost_usd,omitempty"`
}

type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

type SessionListParams struct {
	RunDir string `json:"runDir,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type SessionInfo struct {
	SessionID  string `json:"sessionId"`
	RunID      string `json:"runId"`
	RunDir     string `json:"runDir,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	State      string `json:"state"`
	EndReason  string `json:"endReason,omitempty"`
	LastUpdate string `json:"lastUpdated,omitempty"`
	TracePath  string `json:"tracePath,omitempty"`
	Active     bool   `json:"active"`
	Restorable bool   `json:"restorable"`
}

type SessionListResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SessionResumeParams struct {
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	RunDir    string `json:"runDir,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

type SessionResumeResult struct {
	SessionID string `json:"sessionId"`
	RunID     string `json:"runId"`
}

type SessionReconfigureParams struct {
	SessionID string `json:"sessionId"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Solver    string `json:"solver,omitempty"`
}

type SessionReconfigureResult struct {
	SessionID string `json:"sessionId"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Solver    string `json:"solver"`
}

type SuggestedPrompt struct {
	ID          string   `json:"id,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Prompt      string   `json:"prompt"`
	Tags        []string `json:"tags,omitempty"`
}

type SetSuggestedPromptsParams struct {
	SessionID string            `json:"sessionId,omitempty"`
	Prompts   []SuggestedPrompt `json:"prompts"`
}

type SetSuggestedPromptsResult struct {
	Count int `json:"count"`
}

type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type SessionPromptResult struct {
	StopReason string `json:"stopReason"` // ACP spec: "end_turn", "max_tokens", "max_turn_requests", "refusal", "cancelled"
}

type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

type SessionUpdateParams struct {
	SessionID string `json:"sessionId"`
	Update    Update `json:"update"`
}

type Update struct {
	Type              string          `json:"sessionUpdate"` // discriminator: agent_message_chunk, agent_thought_chunk, tool_call, tool_call_update, available_commands_update
	Content           *ContentBlock   `json:"content,omitempty"`
	ToolCallID        string          `json:"toolCallId,omitempty"`
	Title             string          `json:"title,omitempty"`
	Kind              string          `json:"kind,omitempty"`   // "read", "edit", "execute"
	Status            string          `json:"status,omitempty"` // "pending", "in_progress", "completed", "failed"
	Locations         []string        `json:"locations,omitempty"`
	RawInput          json.RawMessage `json:"rawInput,omitempty"`
	RawOutput         json.RawMessage `json:"rawOutput,omitempty"`
	AvailableCommands []Command       `json:"availableCommands,omitempty"`
}

type Command struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
