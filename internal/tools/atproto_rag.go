package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
	"github.com/tripledoublev/v100/internal/memory"
	"github.com/tripledoublev/v100/internal/providers"
)

const atprotoStoreName = "atproto"

// embedProvider returns the embedding provider from the call context,
// preferring EmbedProvider over the chat Provider.
func embedProvider(call ToolCallContext) providers.Provider {
	if call.EmbedProvider != nil {
		return call.EmbedProvider
	}
	return call.Provider
}

// ---------------------------------------------------------------------------
// atproto_index — fetch ATProto records and store as vector embeddings
// ---------------------------------------------------------------------------

type atprotoIndexTool struct{ cfg *config.Config }

// ATProtoIndex returns the atproto_index tool.
func ATProtoIndex(cfg *config.Config) Tool { return &atprotoIndexTool{cfg: cfg} }

func (t *atprotoIndexTool) Name() string { return "atproto_index" }
func (t *atprotoIndexTool) Description() string {
	return "Fetch ATProto/Bluesky records (feed, notifications, or a user profile) and store them as vector embeddings for later semantic retrieval with atproto_recall."
}
func (t *atprotoIndexTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoIndexTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, MutatesRunState: true}
}

func (t *atprotoIndexTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["source"],
		"properties": {
			"source": {
				"type": "string",
				"enum": ["feed", "notifications", "profile"],
				"description": "What to index: feed (home timeline), notifications, or a user profile."
			},
			"limit": {
				"type": "integer",
				"description": "Number of records to fetch and embed (1–100, default 20)."
			},
			"handle": {
				"type": "string",
				"description": "Bluesky handle to fetch profile for (source=profile only). Defaults to the authenticated user."
			}
		}
	}`)
}

func (t *atprotoIndexTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"indexed":{"type":"integer"}}}`)
}

func (t *atprotoIndexTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var in struct {
		Source string `json:"source"`
		Limit  int    `json:"limit"`
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Limit > 100 {
		in.Limit = 100
	}
	ep := embedProvider(call)
	if ep == nil {
		return failResult(start, "no embedding provider available; use --embedding <provider>"), nil
	}

	cli := newATProtoClient(t.cfg.ATProto)
	if err := cli.login(); err != nil {
		return failResult(start, err.Error()), nil
	}

	records, err := fetchRecordsForIndexing(cli, in.Source, in.Limit, in.Handle)
	if err != nil {
		return failResult(start, err.Error()), nil
	}

	store := memory.NewNamedVectorStore(config.UserDataDir(), atprotoStoreName)
	_ = store.Load()

	indexed := 0
	for _, r := range records {
		embResp, embErr := ep.Embed(ctx, providers.EmbedRequest{Text: r.text, Model: t.cfg.Embedding.Model})
		if embErr != nil {
			return failResult(start, fmt.Sprintf("embedding record: %s (use --embedding <provider> to specify an embedding provider)", embErr)), nil
		}
		item := memory.MemoryItem{
			ID:        fmt.Sprintf("atproto-%x", time.Now().UnixNano()),
			Content:   r.text,
			Category:  "note",
			Embedding: embResp.Embedding,
			Metadata: memory.Metadata{
				Tags: map[string]string{
					"record_type": r.recordType,
					"uri":         r.uri,
					"author":      r.author,
				},
			},
			TS: time.Now().UTC(),
		}
		if addErr := store.Add(item); addErr != nil {
			continue
		}
		indexed++
	}

	return ToolResult{
		OK:         true,
		Output:     fmt.Sprintf(`{"indexed":%d}`, indexed),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// indexRecord is a minimal record for embedding.
type indexRecord struct {
	text       string
	uri        string
	author     string
	recordType string
}

func fetchRecordsForIndexing(cli *atProtoClient, source string, limit int, handle string) ([]indexRecord, error) {
	switch source {
	case "feed":
		return fetchFeedRecords(cli, limit)
	case "notifications":
		return fetchNotificationRecords(cli, limit)
	case "profile":
		return fetchProfileRecord(cli, handle)
	default:
		return nil, fmt.Errorf("unknown source %q; must be feed, notifications, or profile", source)
	}
}

func fetchFeedRecords(cli *atProtoClient, limit int) ([]indexRecord, error) {
	params := url.Values{"limit": {fmt.Sprintf("%d", limit)}}
	data, err := cli.xrpcGet("app.bsky.feed.getTimeline", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Feed []struct {
			Post struct {
				URI    string `json:"uri"`
				Author struct {
					Handle      string `json:"handle"`
					DisplayName string `json:"displayName"`
				} `json:"author"`
				Record struct {
					Text      string `json:"text"`
					CreatedAt string `json:"createdAt"`
				} `json:"record"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	out := make([]indexRecord, 0, len(resp.Feed))
	for _, item := range resp.Feed {
		p := item.Post
		if p.Record.Text == "" {
			continue
		}
		author := p.Author.Handle
		if p.Author.DisplayName != "" {
			author = p.Author.DisplayName + " (@" + p.Author.Handle + ")"
		}
		out = append(out, indexRecord{
			text:       fmt.Sprintf("%s: %s", author, p.Record.Text),
			uri:        p.URI,
			author:     p.Author.Handle,
			recordType: "post",
		})
	}
	return out, nil
}

func fetchNotificationRecords(cli *atProtoClient, limit int) ([]indexRecord, error) {
	params := url.Values{"limit": {fmt.Sprintf("%d", limit)}}
	data, err := cli.xrpcGet("app.bsky.notification.listNotifications", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Notifications []struct {
			URI    string `json:"uri"`
			Reason string `json:"reason"`
			Author struct {
				Handle      string `json:"handle"`
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Record struct {
				Text string `json:"text"`
			} `json:"record"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse notifications: %w", err)
	}
	out := make([]indexRecord, 0, len(resp.Notifications))
	for _, n := range resp.Notifications {
		author := "@" + n.Author.Handle
		if n.Author.DisplayName != "" {
			author = n.Author.DisplayName + " (" + author + ")"
		}
		text := strings.TrimSpace(n.Record.Text)
		content := fmt.Sprintf("[%s] %s", n.Reason, author)
		if text != "" {
			content += ": " + text
		}
		out = append(out, indexRecord{
			text:       content,
			uri:        n.URI,
			author:     n.Author.Handle,
			recordType: "notification",
		})
	}
	return out, nil
}

func fetchProfileRecord(cli *atProtoClient, handle string) ([]indexRecord, error) {
	if handle == "" {
		handle = cli.cfg.Handle
	}
	params := url.Values{"actor": {handle}}
	data, err := cli.xrpcGet("app.bsky.actor.getProfile", params)
	if err != nil {
		return nil, err
	}
	var p struct {
		DID         string `json:"did"`
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	name := p.Handle
	if p.DisplayName != "" {
		name = p.DisplayName + " (@" + p.Handle + ")"
	}
	text := name
	if p.Description != "" {
		text += ": " + p.Description
	}
	return []indexRecord{{
		text:       text,
		uri:        "at://" + p.DID + "/app.bsky.actor.profile/self",
		author:     p.Handle,
		recordType: "profile",
	}}, nil
}

// ---------------------------------------------------------------------------
// atproto_recall — semantic search over indexed ATProto records
// ---------------------------------------------------------------------------

type atprotoRecallTool struct{ cfg *config.Config }

// ATProtoRecall returns the atproto_recall tool.
func ATProtoRecall(cfg *config.Config) Tool { return &atprotoRecallTool{cfg: cfg} }

func (t *atprotoRecallTool) Name() string { return "atproto_recall" }
func (t *atprotoRecallTool) Description() string {
	return "Semantic search over ATProto/Bluesky records previously indexed with atproto_index. Returns the most relevant posts, notifications, or profiles as RAG context."
}
func (t *atprotoRecallTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoRecallTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true}
}

func (t *atprotoRecallTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["query"],
		"properties": {
			"query": {
				"type": "string",
				"description": "Natural language query to search for in indexed records."
			},
			"limit": {
				"type": "integer",
				"description": "Number of results to return (default 5)."
			},
			"record_type": {
				"type": "string",
				"enum": ["post", "notification", "profile"],
				"description": "Filter results to a specific record type (optional)."
			}
		}
	}`)
}

func (t *atprotoRecallTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"results":{"type":"array"}}}`)
}

func (t *atprotoRecallTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()
	var in struct {
		Query      string `json:"query"`
		Limit      int    `json:"limit"`
		RecordType string `json:"record_type"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	if in.Query == "" {
		return failResult(start, "query is required"), nil
	}
	if in.Limit <= 0 {
		in.Limit = 5
	}
	ep := embedProvider(call)
	if ep == nil {
		return failResult(start, "no embedding provider available; use --embedding <provider>"), nil
	}

	embResp, err := ep.Embed(ctx, providers.EmbedRequest{Text: in.Query, Model: t.cfg.Embedding.Model})
	if err != nil {
		return failResult(start, "embedding query: "+err.Error()), nil
	}

	store := memory.NewNamedVectorStore(config.UserDataDir(), atprotoStoreName)
	if err := store.Load(); err != nil {
		return failResult(start, "load vector store: "+err.Error()), nil
	}

	// Fetch more than needed when filtering so we still get `limit` results.
	fetchK := in.Limit
	if in.RecordType != "" {
		fetchK = in.Limit * 5
	}
	raw := store.Search(embResp.Embedding, fetchK)

	results := raw
	if in.RecordType != "" {
		results = results[:0]
		for _, r := range raw {
			if r.Item.Metadata.Tags["record_type"] == in.RecordType {
				results = append(results, r)
				if len(results) >= in.Limit {
					break
				}
			}
		}
	}

	b, err := json.Marshal(map[string]any{"results": results})
	if err != nil {
		return failResult(start, "marshal results: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}
