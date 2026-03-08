package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/auth"
)

const (
	codexEndpoint     = "https://chatgpt.com/backend-api/codex/responses"
	codexDefaultModel = "gpt-5.4"
)

// CodexProvider implements Provider using the ChatGPT subscription backend.
// It reads (and auto-refreshes) credentials from ~/.config/v100/auth.json.
type CodexProvider struct {
	mu           sync.Mutex
	token        auth.Token
	tokenPath    string
	defaultModel string
	client       *http.Client
}

// NewCodexProvider creates a provider that loads its OAuth token from tokenPath.
// Pass "" for tokenPath to use auth.DefaultTokenPath().
func NewCodexProvider(tokenPath, defaultModel string) (*CodexProvider, error) {
	if tokenPath == "" {
		tokenPath = auth.DefaultTokenPath()
	}
	if defaultModel == "" {
		defaultModel = codexDefaultModel
	}
	p := &CodexProvider{
		tokenPath:    tokenPath,
		defaultModel: defaultModel,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
	t, err := auth.Load(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("codex: %w\n  → run 'v100 login' to authenticate", err)
	}
	p.token = *t
	return p, nil
}

func (p *CodexProvider) Name() string { return "codex" }

func (p *CodexProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true}
}

// accessToken returns a valid access token + accountID, refreshing if expired.
func (p *CodexProvider) accessToken(ctx context.Context) (access, accountID string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.token.Valid() {
		refreshed, err := auth.Refresh(ctx, p.token.Refresh)
		if err != nil {
			return "", "", fmt.Errorf("codex: token refresh failed: %w\n  → run 'v100 login' to re-authenticate", err)
		}
		if saveErr := auth.Save(p.tokenPath, refreshed); saveErr != nil {
			// Non-fatal: continue with the refreshed token even if save fails
			fmt.Printf("codex: warning: could not save refreshed token: %v\n", saveErr)
		}
		p.token = *refreshed
	}
	return p.token.Access, p.token.AccountID, nil
}

// ─────────────────────────────────────────
// Complete
// ─────────────────────────────────────────

func (p *CodexProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	access, accountID, err := p.accessToken(ctx)
	if err != nil {
		return CompleteResponse{}, err
	}

	instructions, input := codexConvertMessages(req.Messages)

	var tools []codexToolDef
	for _, ts := range req.Tools {
		tools = append(tools, codexToolDef{
			Type:        "function",
			Name:        ts.Name,
			Description: ts.Description,
			Parameters:  ts.InputSchema,
		})
	}

	cReq := codexRequest{
		Model:        model,
		Instructions: instructions,
		Input:        input,
		Tools:        tools,
		Stream:       true,
		Store:        false,
	}
	if req.GenParams.Temperature != nil {
		cReq.Temperature = req.GenParams.Temperature
	}
	if req.GenParams.TopP != nil {
		cReq.TopP = req.GenParams.TopP
	}
	if req.GenParams.MaxTokens > 0 {
		cReq.MaxTokens = req.GenParams.MaxTokens
	}
	body, err := json.Marshal(cReq)
	if err != nil {
		return CompleteResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, codexEndpoint, bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("Openai-Account-Id", accountID)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("codex: request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		raw, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return CompleteResponse{}, fmt.Errorf("read error body: %w", err)
		}
		baseErr := fmt.Errorf("codex: HTTP %d: %s", httpResp.StatusCode, raw)
		if httpResp.StatusCode == http.StatusTooManyRequests || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			return CompleteResponse{}, &RetryableError{
				Err:        baseErr,
				StatusCode: httpResp.StatusCode,
				RetryAfter: retryAfterFromHeader(httpResp.Header.Get("Retry-After")),
			}
		}
		return CompleteResponse{}, baseErr
	}

	return codexParseStream(httpResp.Body)
}

// ─────────────────────────────────────────
// Request types (Responses API format)
// ─────────────────────────────────────────

type codexRequest struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions,omitempty"`
	Input        []codexInput   `json:"input"`
	Tools        []codexToolDef `json:"tools,omitempty"`
	Stream       bool           `json:"stream"`
	Store        bool           `json:"store"`
	Temperature  *float64       `json:"temperature,omitempty"`
	TopP         *float64       `json:"top_p,omitempty"`
	MaxTokens    int            `json:"max_output_tokens,omitempty"`
}

type codexInput struct {
	// For user/assistant messages
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
	// For function_call / function_call_output items
	Type      string  `json:"type,omitempty"`
	CallID    string  `json:"call_id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Arguments string  `json:"arguments,omitempty"`
	Output    *string `json:"output,omitempty"`
}

type codexToolDef struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// codexConvertMessages converts provider messages to Responses API format.
// Returns (instructions, input) where instructions = system prompt.
func codexConvertMessages(msgs []Message) (string, []codexInput) {
	var instructions string
	var input []codexInput

	for _, m := range msgs {
		switch m.Role {
		case "system":
			instructions = m.Content

		case "user":
			input = append(input, codexInput{
				Role:    "user",
				Content: m.Content,
			})

		case "assistant":
			if m.Content != "" {
				input = append(input, codexInput{
					Role: "assistant",
					Content: []map[string]any{{
						"type": "output_text",
						"text": m.Content,
					}},
				})
			}
			// Prior tool calls must be replayed as top-level function_call items.
			for _, tc := range m.ToolCalls {
				input = append(input, codexInput{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: string(tc.Args),
				})
			}

		case "tool":
			out := m.Content
			if out == "" {
				out = "(no output)"
			}
			input = append(input, codexInput{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: &out,
			})
		}
	}
	return instructions, input
}

// ─────────────────────────────────────────
// SSE stream parser
// ─────────────────────────────────────────

func codexParseStream(r io.Reader) (CompleteResponse, error) {
	var (
		text      strings.Builder
		toolCalls []ToolCall
		usage     Usage
	)

	type pendingCall struct {
		id   string
		name string
		args strings.Builder
	}
	pending := map[int]*pendingCall{}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		switch eventType {
		case "response.output_text.delta":
			var d struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &d) == nil {
				text.WriteString(d.Delta)
			}

		case "response.output_item.added":
			var ev struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Name   string `json:"name"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Item.Type == "function_call" {
				pending[ev.OutputIndex] = &pendingCall{
					id:   ev.Item.CallID,
					name: ev.Item.Name,
				}
			}

		case "response.function_call_arguments.delta":
			var d struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &d) == nil {
				if pc, ok := pending[d.OutputIndex]; ok {
					pc.args.WriteString(d.Delta)
				}
			}

		case "response.output_item.done":
			var ev struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && ev.Item.Type == "function_call" {
				args := ev.Item.Arguments
				if pc, ok := pending[ev.OutputIndex]; ok {
					if pc.args.Len() > 0 {
						args = pc.args.String()
					}
					delete(pending, ev.OutputIndex)
				}
				toolCalls = append(toolCalls, ToolCall{
					ID:   ev.Item.CallID,
					Name: ev.Item.Name,
					Args: json.RawMessage(args),
				})
			}

		case "response.completed":
			var ev struct {
				Response struct {
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				usage.InputTokens = ev.Response.Usage.InputTokens
				usage.OutputTokens = ev.Response.Usage.OutputTokens
				usage.CostUSD = 0 // subscription — no API cost
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return CompleteResponse{}, fmt.Errorf("codex: stream: %w", err)
	}

	raw, _ := json.Marshal(map[string]any{"streamed": true})
	return CompleteResponse{
		AssistantText: text.String(),
		ToolCalls:     toolCalls,
		Usage:         usage,
		Raw:           raw,
	}, nil
}

func (p *CodexProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	// Subscription-backed ChatGPT embeddings endpoint is not currently known/supported in this harness.
	return EmbedResponse{}, fmt.Errorf("codex: embeddings not yet supported for subscription provider")
}

func (p *CodexProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}
	return ModelMetadata{
		Model:       model,
		ContextSize: 128000,
		IsFree:      true, // subscription-backed
	}, nil
}
