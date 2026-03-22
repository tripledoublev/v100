package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tripledoublev/v100/internal/auth"
)

const (
	geminiBaseURL      = "https://cloudcode-pa.googleapis.com"
	geminiDefaultModel = "gemini-2.5-flash" // also: gemini-2.5-pro, gemini-3-pro-preview, gemini-3-flash-preview
)

// GeminiProvider implements Provider using Google's Code Assist API
// with a Gemini Pro / Google One AI Premium subscription (no API billing).
type GeminiProvider struct {
	mu           sync.Mutex
	token        auth.GeminiToken
	tokenPath    string
	defaultModel string
	client       *http.Client
}

// NewGeminiProvider creates a provider that loads its OAuth token from tokenPath.
func NewGeminiProvider(tokenPath, defaultModel string) (*GeminiProvider, error) {
	if tokenPath == "" {
		tokenPath = auth.DefaultGeminiTokenPath()
	}
	if defaultModel == "" {
		defaultModel = geminiDefaultModel
	}
	p := &GeminiProvider{
		tokenPath:    tokenPath,
		defaultModel: defaultModel,
		client:       &http.Client{Timeout: 120 * time.Second},
	}
	t, err := auth.LoadGemini(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w\n  → run 'v100 login --provider gemini' to authenticate", err)
	}
	p.token = *t
	return p, nil
}

func (p *GeminiProvider) Name() string { return "gemini" }

func (p *GeminiProvider) Capabilities() Capabilities {
	return Capabilities{ToolCalls: true, JSONMode: false, Streaming: true}
}

// accessToken returns a valid access token, refreshing if expired.
func (p *GeminiProvider) accessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.token.Valid() {
		refreshed, err := auth.RefreshGemini(ctx, p.token.Refresh)
		if err != nil {
			return "", fmt.Errorf("gemini: token refresh failed: %w\n  → run 'v100 login --provider gemini' to re-authenticate", err)
		}
		p.token.Access = refreshed.Access
		p.token.ExpiresMS = refreshed.ExpiresMS
		if refreshed.Refresh != "" {
			p.token.Refresh = refreshed.Refresh
		}
		if saveErr := auth.SaveGemini(p.tokenPath, &p.token); saveErr != nil {
			fmt.Printf("gemini: warning: could not save refreshed token: %v\n", saveErr)
		}
	}
	return p.token.Access, nil
}

// geminiClientMetadata is sent with Code Assist lifecycle calls.
type geminiClientMetadata struct {
	IdeType    string `json:"ideType,omitempty"`
	Platform   string `json:"platform,omitempty"`
	PluginType string `json:"pluginType,omitempty"`
}

var defaultClientMetadata = geminiClientMetadata{
	IdeType:    "IDE_UNSPECIFIED",
	Platform:   "PLATFORM_UNSPECIFIED",
	PluginType: "GEMINI",
}

// onboard calls the Code Assist API to look up or provision the user's GCP project.
func (p *GeminiProvider) onboard(ctx context.Context, access string) (string, error) {
	// Try loadCodeAssist first — most users already have a project
	projectID, tierID, err := p.loadCodeAssist(ctx, access)
	if err == nil && projectID != "" {
		return projectID, nil
	}

	// Need to onboard — use FREE tier if no tier was detected
	if tierID == "" {
		tierID = "FREE"
	}
	projectID, err = p.onboardUser(ctx, access, tierID)
	if err != nil {
		return "", fmt.Errorf("gemini: onboard: %w", err)
	}
	return projectID, nil
}

func (p *GeminiProvider) loadCodeAssist(ctx context.Context, access string) (projectID, tierID string, err error) {
	body, _ := json.Marshal(map[string]any{
		"metadata": defaultClientMetadata,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiBaseURL+"/v1internal:loadCodeAssist", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("loadCodeAssist HTTP %d: %s", resp.StatusCode, raw)
	}

	var result struct {
		CloudaicompanionProject string `json:"cloudaicompanionProject"`
		CurrentTier             *struct {
			ID string `json:"id"`
		} `json:"currentTier"`
		PaidTier *struct {
			ID string `json:"id"`
		} `json:"paidTier"`
		AllowedTiers []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", "", err
	}

	// Extract project ID
	projectID = result.CloudaicompanionProject

	// Extract tier: prefer paidTier, then currentTier
	if result.PaidTier != nil && result.PaidTier.ID != "" {
		tierID = result.PaidTier.ID
	} else if result.CurrentTier != nil && result.CurrentTier.ID != "" {
		tierID = result.CurrentTier.ID
	}

	// If no current tier, pick default from allowedTiers (for onboarding)
	if tierID == "" {
		for _, t := range result.AllowedTiers {
			if t.IsDefault {
				tierID = t.ID
				break
			}
		}
	}

	return projectID, tierID, nil
}

func (p *GeminiProvider) onboardUser(ctx context.Context, access, tierID string) (string, error) {
	reqBody := map[string]any{
		"tierId":   tierID,
		"metadata": defaultClientMetadata,
	}
	// FREE tier: Google manages the project; don't send cloudaicompanionProject
	body, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, geminiBaseURL+"/v1internal:onboardUser", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("onboardUser HTTP %d: %s", resp.StatusCode, raw)
	}

	// Response is a long-running operation
	var lro struct {
		Done     bool `json:"done"`
		Response *struct {
			CloudaicompanionProject *struct {
				ID string `json:"id"`
			} `json:"cloudaicompanionProject"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &lro); err != nil {
		return "", err
	}

	if lro.Response != nil && lro.Response.CloudaicompanionProject != nil {
		return lro.Response.CloudaicompanionProject.ID, nil
	}
	return "", fmt.Errorf("onboardUser: no project ID in response: %s", raw)
}

// ─────────────────────────────────────────
// Complete
// ─────────────────────────────────────────

func (p *GeminiProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	access, err := p.accessToken(ctx)
	if err != nil {
		return CompleteResponse{}, err
	}

	// Ensure we have a project ID (and tier)
	p.mu.Lock()
	if p.token.ProjectID == "" {
		projectID, tierID, err := p.loadCodeAssist(ctx, access)
		if err == nil && projectID != "" {
			p.token.ProjectID = projectID
			p.token.TierID = tierID
		} else {
			projectID, err = p.onboard(ctx, access)
			if err != nil {
				p.mu.Unlock()
				return CompleteResponse{}, err
			}
			p.token.ProjectID = projectID
		}
		if saveErr := auth.SaveGemini(p.tokenPath, &p.token); saveErr != nil {
			fmt.Printf("gemini: warning: could not save project ID: %v\n", saveErr)
		}
	}
	projectID := p.token.ProjectID
	p.mu.Unlock()

	sysInstruction, contents := geminiConvertMessages(req.Messages)

	var tools []geminiToolDef
	if len(req.Tools) > 0 {
		var funcDecls []geminiFunctionDecl
		for _, ts := range req.Tools {
			funcDecls = append(funcDecls, geminiFunctionDecl{
				Name:        ts.Name,
				Description: ts.Description,
				Parameters:  ts.InputSchema,
			})
		}
		tools = []geminiToolDef{{FunctionDeclarations: funcDecls}}
	}

	promptID := fmt.Sprintf("%x", time.Now().UnixNano())

	var genCfg *geminiGenerationConfig
	gp := req.GenParams
	if gp.Temperature != nil || gp.TopP != nil || gp.TopK != nil || gp.MaxTokens > 0 || len(gp.StopSequences) > 0 || gp.Seed != nil {
		genCfg = &geminiGenerationConfig{
			Temperature:     gp.Temperature,
			TopP:            gp.TopP,
			TopK:            gp.TopK,
			MaxOutputTokens: gp.MaxTokens,
			StopSequences:   gp.StopSequences,
			Seed:            gp.Seed,
		}
	}

	envelope := geminiEnvelope{
		Model:              model,
		Project:            projectID,
		UserPromptID:       promptID,
		EnabledCreditTypes: []string{"GOOGLE_ONE_AI"},
		Request: geminiRequest{
			Contents:          contents,
			SystemInstruction: sysInstruction,
			Tools:             tools,
			GenerationConfig:  genCfg,
		},
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return CompleteResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		geminiBaseURL+"/v1internal:streamGenerateContent?alt=sse",
		bytes.NewReader(body))
	if err != nil {
		return CompleteResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("gemini: request: %w", err)
	}

	if httpResp.StatusCode == http.StatusOK {
		resp, err := geminiParseSSE(httpResp.Body)
		_ = httpResp.Body.Close()
		return resp, err
	}

	raw, err := io.ReadAll(httpResp.Body)
	_ = httpResp.Body.Close()
	if err != nil {
		return CompleteResponse{}, fmt.Errorf("read body: %w", err)
	}

	baseErr := geminiFormatError(httpResp.StatusCode, raw)
	if httpResp.StatusCode == http.StatusTooManyRequests || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
		retryAfter := retryAfterFromHeader(httpResp.Header.Get("Retry-After"))
		if retryAfter == 0 && httpResp.StatusCode == http.StatusTooManyRequests {
			retryAfter = geminiParseRetryWait(raw)
		}
		return CompleteResponse{}, &RetryableError{
			Err:        baseErr,
			StatusCode: httpResp.StatusCode,
			RetryAfter: retryAfter,
		}
	}

	return CompleteResponse{}, baseErr
}

// ─────────────────────────────────────────
// Request types (Gemini native format)
// ─────────────────────────────────────────

type geminiEnvelope struct {
	Model              string        `json:"model"`
	Project            string        `json:"project,omitempty"`
	UserPromptID       string        `json:"user_prompt_id"`
	EnabledCreditTypes []string      `json:"enabled_credit_types,omitempty"`
	Request            geminiRequest `json:"request"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	TopK            *int     `json:"topK,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
	Seed            *int     `json:"seed,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDef         `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string              `json:"text,omitempty"`
	InlineData       *geminiInlineData   `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFuncResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFuncResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolDef struct {
	FunctionDeclarations []geminiFunctionDecl `json:"functionDeclarations"`
}

type geminiFunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// geminiConvertMessages converts provider messages to Gemini format.
// Returns (systemInstruction, contents).
func geminiConvertMessages(msgs []Message) (*geminiContent, []geminiContent) {
	var sysInstruction *geminiContent
	var contents []geminiContent
	var pendingToolResponses []geminiPart

	flushToolResponses := func() {
		if len(pendingToolResponses) == 0 {
			return
		}
		contents = append(contents, geminiContent{
			Role:  "user",
			Parts: pendingToolResponses,
		})
		pendingToolResponses = nil
	}

	for _, m := range msgs {
		switch m.Role {
		case "system":
			flushToolResponses()
			sysInstruction = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}

		case "user":
			flushToolResponses()
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, img := range m.Images {
				if len(img.Data) == 0 {
					continue
				}
				mimeType := strings.TrimSpace(img.MIMEType)
				if mimeType == "" {
					mimeType = "image/png"
				}
				parts = append(parts, geminiPart{
					InlineData: &geminiInlineData{
						MIMEType: mimeType,
						Data:     base64.StdEncoding.EncodeToString(img.Data),
					},
				})
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: m.Content}}
			}
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: parts,
			})

		case "assistant":
			flushToolResponses()
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Name,
						Args: tc.Args,
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{
					Role:  "model",
					Parts: parts,
				})
			}

		case "tool":
			content := m.Content
			if content == "" {
				content = "(no output)"
			}
			// Gemini expects tool results to come back as functionResponse parts in a
			// single user turn matching the prior function-call turn.
			pendingToolResponses = append(pendingToolResponses, geminiPart{
				FunctionResponse: &geminiFuncResponse{
					Name:     m.Name,
					Response: map[string]any{"result": content},
				},
			})
		}
	}
	flushToolResponses()
	return sysInstruction, contents
}

// ─────────────────────────────────────────
// SSE stream parser
// ─────────────────────────────────────────

// geminiSSEChunk is a single SSE data chunk from streamGenerateContent.
type geminiSSEChunk struct {
	Response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	} `json:"response"`
}

func geminiParseSSE(r io.Reader) (CompleteResponse, error) {
	var (
		textBuf   strings.Builder
		toolCalls []ToolCall
		usage     Usage
	)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk geminiSSEChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		resp := chunk.Response

		// Accumulate text and tool calls from candidates
		if len(resp.Candidates) > 0 {
			for _, part := range resp.Candidates[0].Content.Parts {
				if part.Text != "" {
					textBuf.WriteString(part.Text)
				}
				if part.FunctionCall != nil {
					args := part.FunctionCall.Args
					if args == nil {
						args = json.RawMessage("{}")
					}
					toolCalls = append(toolCalls, ToolCall{
						ID:   fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(toolCalls)),
						Name: part.FunctionCall.Name,
						Args: args,
					})
				}
			}
		}

		// Update usage from the last chunk that has it
		if resp.UsageMetadata.PromptTokenCount > 0 || resp.UsageMetadata.CandidatesTokenCount > 0 {
			usage.InputTokens = resp.UsageMetadata.PromptTokenCount
			usage.OutputTokens = resp.UsageMetadata.CandidatesTokenCount
			usage.CostUSD = 0 // subscription — no API cost
		}
	}

	if err := scanner.Err(); err != nil {
		return CompleteResponse{}, fmt.Errorf("gemini: stream: %w", err)
	}

	raw, _ := json.Marshal(map[string]any{"streamed": true})
	return CompleteResponse{
		AssistantText: textBuf.String(),
		ToolCalls:     toolCalls,
		Usage:         usage,
		Raw:           raw,
	}, nil
}

// StreamComplete implements Streamer for real-time token delivery.
func (p *GeminiProvider) StreamComplete(ctx context.Context, req CompleteRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.defaultModel
	}

	access, err := p.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	if p.token.ProjectID == "" {
		projectID, tierID, err := p.loadCodeAssist(ctx, access)
		if err == nil && projectID != "" {
			p.token.ProjectID = projectID
			p.token.TierID = tierID
		} else {
			projectID, err = p.onboard(ctx, access)
			if err != nil {
				p.mu.Unlock()
				return nil, err
			}
			p.token.ProjectID = projectID
		}
		if saveErr := auth.SaveGemini(p.tokenPath, &p.token); saveErr != nil {
			fmt.Printf("gemini: warning: could not save project ID: %v\n", saveErr)
		}
	}
	projectID := p.token.ProjectID
	p.mu.Unlock()

	sysInstruction, contents := geminiConvertMessages(req.Messages)

	var tools []geminiToolDef
	if len(req.Tools) > 0 {
		var funcDecls []geminiFunctionDecl
		for _, ts := range req.Tools {
			funcDecls = append(funcDecls, geminiFunctionDecl{
				Name:        ts.Name,
				Description: ts.Description,
				Parameters:  ts.InputSchema,
			})
		}
		tools = []geminiToolDef{{FunctionDeclarations: funcDecls}}
	}

	promptID := fmt.Sprintf("%x", time.Now().UnixNano())

	var genCfg *geminiGenerationConfig
	gp := req.GenParams
	if gp.Temperature != nil || gp.TopP != nil || gp.TopK != nil || gp.MaxTokens > 0 || len(gp.StopSequences) > 0 || gp.Seed != nil {
		genCfg = &geminiGenerationConfig{
			Temperature:     gp.Temperature,
			TopP:            gp.TopP,
			TopK:            gp.TopK,
			MaxOutputTokens: gp.MaxTokens,
			StopSequences:   gp.StopSequences,
			Seed:            gp.Seed,
		}
	}

	envelope := geminiEnvelope{
		Model:              model,
		Project:            projectID,
		UserPromptID:       promptID,
		EnabledCreditTypes: []string{"GOOGLE_ONE_AI"},
		Request: geminiRequest{
			Contents:          contents,
			SystemInstruction: sysInstruction,
			Tools:             tools,
			GenerationConfig:  genCfg,
		},
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		geminiBaseURL+"/v1internal:streamGenerateContent?alt=sse",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+access)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: request: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		baseErr := geminiFormatError(httpResp.StatusCode, raw)
		if httpResp.StatusCode == http.StatusTooManyRequests || (httpResp.StatusCode >= 500 && httpResp.StatusCode < 600) {
			retryAfter := retryAfterFromHeader(httpResp.Header.Get("Retry-After"))
			if retryAfter == 0 && httpResp.StatusCode == http.StatusTooManyRequests {
				retryAfter = geminiParseRetryWait(raw)
			}
			return nil, &RetryableError{Err: baseErr, StatusCode: httpResp.StatusCode, RetryAfter: retryAfter}
		}
		return nil, baseErr
	}

	ch := make(chan StreamEvent, 100)
	go geminiStreamSSE(httpResp, ch)
	return ch, nil
}

func geminiStreamSSE(httpResp *http.Response, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() { _ = httpResp.Body.Close() }()

	scanner := bufio.NewScanner(httpResp.Body)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	toolCallIdx := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk geminiSSEChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		resp := chunk.Response

		if len(resp.Candidates) > 0 {
			for _, part := range resp.Candidates[0].Content.Parts {
				if part.Text != "" {
					ch <- StreamEvent{Type: StreamToken, Text: part.Text}
				}
				if part.FunctionCall != nil {
					args := part.FunctionCall.Args
					if args == nil {
						args = json.RawMessage("{}")
					}
					callID := fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, toolCallIdx)
					toolCallIdx++
					ch <- StreamEvent{Type: StreamToolCallStart, ToolCallID: callID, ToolCallName: part.FunctionCall.Name}
					ch <- StreamEvent{Type: StreamToolCallDelta, ToolCallID: callID, ToolCallArgs: string(args)}
				}
			}
		}

		if resp.UsageMetadata.PromptTokenCount > 0 || resp.UsageMetadata.CandidatesTokenCount > 0 {
			ch <- StreamEvent{Type: StreamDone, Usage: Usage{
				InputTokens:  resp.UsageMetadata.PromptTokenCount,
				OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
				CostUSD:      0,
			}}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("gemini: stream: %w", err)}
	}
}

func (p *GeminiProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	// Gemini embeddings use the public Gemini API, which requires an API key.
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "text-embedding-004"
	}

	apiKey := resolveGeminiEmbeddingAPIKey()
	if apiKey == "" {
		return EmbedResponse{}, fmt.Errorf(
			"gemini: embeddings require a Gemini API key.\n" +
				"  → Set GEMINI_API_KEY or GOOGLE_API_KEY for embedding requests",
		)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent", model)

	payload := map[string]any{
		"model": "models/" + model,
		"content": map[string]any{
			"parts": []map[string]string{
				{"text": req.Text},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return EmbedResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return EmbedResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("gemini: embed request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return EmbedResponse{}, fmt.Errorf("gemini: embed HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return EmbedResponse{}, fmt.Errorf("gemini: parse embed response: %w", err)
	}

	if len(result.Embedding.Values) == 0 {
		return EmbedResponse{}, fmt.Errorf("gemini: no embedding data in response")
	}

	// Estimate token count (rough approximation: 4 chars per token for English)
	estimatedTokens := len(req.Text) / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}

	return EmbedResponse{
		Embedding: result.Embedding.Values,
		Usage: Usage{
			InputTokens: estimatedTokens,
			CostUSD:     0, // API-key cost tracking is not modeled here yet.
		},
	}, nil
}

func resolveGeminiEmbeddingAPIKey() string {
	if key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); key != "" {
		return key
	}
	return ""
}

func (p *GeminiProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if model == "" {
		model = p.defaultModel
	}
	return ModelMetadata{
		Model:       model,
		ContextSize: 1048576, // 1M
		IsFree:      true,    // subscription-backed
	}, nil
}

// geminiFormatError extracts a human-readable message from a Gemini error response.
// For 429 responses, the JSON often contains a "message" field with user-facing text.
func geminiFormatError(statusCode int, raw []byte) error {
	var errResp struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &errResp) == nil {
		if errResp.Message != "" {
			return fmt.Errorf("gemini: %s", errResp.Message)
		}
		if errResp.Error != nil && errResp.Error.Message != "" {
			return fmt.Errorf("gemini: %s", errResp.Error.Message)
		}
	}
	return fmt.Errorf("gemini: HTTP %d: %s", statusCode, raw)
}

// geminiParseRetryWait extracts wait time from a 429 error body like "after 49s".
var retryAfterRe = regexp.MustCompile(`after (\d+)s`)

func geminiParseRetryWait(body []byte) time.Duration {
	m := retryAfterRe.FindSubmatch(body)
	if len(m) >= 2 {
		if secs, err := strconv.Atoi(string(m[1])); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return 10 * time.Second // default fallback
}
