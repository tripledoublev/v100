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
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

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
	SessionID string `json:"sessionId,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

type SessionNewResult struct {
	SessionID string `json:"sessionId"`
}

type SessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type SessionPromptResult struct {
	StopReason string `json:"stopReason"` // e.g., "end_turn", "max_tokens", "cancelled", "error"
}

type SessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

type SessionUpdateParams struct {
	SessionID string `json:"sessionId"`
	Update    Update `json:"update"`
}

type Update struct {
	Type       string          `json:"sessionUpdate"` // discriminator: agent_message_chunk, agent_thought_chunk, tool_call, tool_call_update
	Content    *ContentBlock   `json:"content,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`   // "read", "edit", "execute"
	Status     string          `json:"status,omitempty"` // "pending", "in_progress", "completed", "failed"
	Locations  []string        `json:"locations,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage `json:"rawOutput,omitempty"`
}
