package providers

import (
	"encoding/json"
	"strings"
)

// Shared Anthropic Messages API types used by both the Anthropic provider
// and MiniMax provider (which exposes an Anthropic-compatible endpoint).

type anthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	System        string             `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Tools         []anthropicToolDef `json:"tools,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
}

type anthropicError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ensureToolResultContiguity reorders messages so that tool results always
// immediately follow the assistant message containing their tool calls.
// This is required by MiniMax (error 2013) and is good practice generally.
// Any non-tool messages that appear between a tool_use and its results are
// moved to after the results block.
func ensureToolResultContiguity(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			out = append(out, m)
			i++
			continue
		}
		// Collect the IDs this assistant turn expects results for.
		needed := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			needed[tc.ID] = true
		}
		out = append(out, m)
		i++
		// Scan ahead: gather matching tool results and any interleaved non-tool messages.
		var results []Message
		var deferred []Message
		for i < len(msgs) && len(needed) > 0 {
			cur := msgs[i]
			if cur.Role == "tool" && needed[cur.ToolCallID] {
				results = append(results, cur)
				delete(needed, cur.ToolCallID)
				i++
			} else if cur.Role == "tool" {
				// Tool result for a different call — emit it later.
				deferred = append(deferred, cur)
				i++
			} else {
				// Non-tool message: defer until after results.
				deferred = append(deferred, cur)
				i++
			}
		}
		out = append(out, results...)
		out = append(out, deferred...)
	}
	return out
}

// anthropicConvertMessages converts provider messages to Anthropic format.
// Returns (system, messages). System messages are extracted; tool results
// are wrapped in tool_result content blocks within user turns.
func anthropicConvertMessages(msgs []Message) (string, []anthropicMessage) {
	msgs = ensureToolResultContiguity(msgs)
	var system string
	var out []anthropicMessage

	// Collect pending tool results to merge into a single user turn
	var pendingResults []anthropicContentBlock

	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		switch m.Role {
		case "system":
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
		case "user":
			out = append(out, anthropicMessage{Role: "user", Content: m.Content})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var content []anthropicContentBlock
				if m.Content != "" {
					content = append(content, anthropicContentBlock{Type: "text", Text: m.Content})
				}
				for _, tc := range m.ToolCalls {
					content = append(content, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Name,
						Input: tc.Args,
					})
				}
				out = append(out, anthropicMessage{Role: "assistant", Content: content})
			} else {
				out = append(out, anthropicMessage{Role: "assistant", Content: m.Content})
			}
		case "tool":
			pendingResults = append(pendingResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})

			// If next message is not a tool result, flush pending results into a user turn
			if i+1 == len(msgs) || msgs[i+1].Role != "tool" {
				out = append(out, anthropicMessage{Role: "user", Content: pendingResults})
				pendingResults = nil
			}
		}
	}
	return system, out
}

// anthropicBuildRequest constructs an anthropicRequest from a CompleteRequest.
func anthropicBuildRequest(model string, req CompleteRequest) anthropicRequest {
	system, messages := anthropicConvertMessages(req.Messages)

	var tools []anthropicToolDef
	for _, ts := range req.Tools {
		tools = append(tools, anthropicToolDef{
			Name:        ts.Name,
			Description: ts.Description,
			InputSchema: ts.InputSchema,
		})
	}

	maxTokens := req.GenParams.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	aReq := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  messages,
	}
	if len(tools) > 0 {
		aReq.Tools = tools
	}
	if req.GenParams.Temperature != nil {
		aReq.Temperature = req.GenParams.Temperature
	}
	if req.GenParams.TopP != nil {
		aReq.TopP = req.GenParams.TopP
	}
	if req.GenParams.TopK != nil {
		aReq.TopK = req.GenParams.TopK
	}
	if len(req.GenParams.StopSequences) > 0 {
		aReq.StopSequences = req.GenParams.StopSequences
	}
	return aReq
}

// anthropicParseResponse parses an Anthropic Messages API response body
// into a CompleteResponse, using costFn to compute CostUSD.
func anthropicParseResponse(raw []byte, costFn func(input, output int) float64) (CompleteResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return CompleteResponse{}, err
	}
	var text strings.Builder
	var toolCalls []ToolCall
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			input := block.Input
			if input == nil {
				input = json.RawMessage("{}")
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   block.ID,
				Name: block.Name,
				Args: input,
			})
		}
	}
	return CompleteResponse{
		AssistantText: text.String(),
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			CostUSD:      costFn(resp.Usage.InputTokens, resp.Usage.OutputTokens),
		},
		Raw: raw,
	}, nil
}
