package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	return "Fetch ATProto/Bluesky records (feed, notifications, a user profile, or a user's posts directly from their PDS) and store them as vector embeddings for later semantic retrieval with atproto_recall."
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
				"enum": ["feed", "notifications", "profile", "user_posts"],
				"description": "What to index: feed (home timeline), notifications, a user profile, or user_posts (fetches posts directly from a user's PDS — works even when the appview/Bsky.app is down)."
			},
			"limit": {
				"type": "integer",
				"description": "Number of records to fetch and embed (1–100, default 20)."
			},
			"handle": {
				"type": "string",
				"description": "Bluesky handle (required for source=user_posts, optional for source=profile to default to authenticated user)."
			}
		}
	}`)
}

func (t *atprotoIndexTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"indexed":{"type":"integer"},"skipped":{"type":"integer"}}}`)
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
	// user_posts does not require appview login — it talks directly to the
	// target user's PDS. We still try login for other sources.
	if in.Source != "user_posts" {
		if err := cli.login(); err != nil {
			return failResult(start, err.Error()), nil
		}
	}

	records, err := fetchRecordsForIndexing(cli, in.Source, in.Limit, in.Handle)
	if err != nil {
		return failResult(start, err.Error()), nil
	}

	store := memory.NewNamedVectorStore(config.UserDataDir(), atprotoStoreName)
	_ = store.Load()

	indexed := 0
	skipped := 0
	for _, r := range records {
		// Dedup: skip records whose URI is already in the store.
		if r.uri != "" && store.HasTag("uri", r.uri) {
			skipped++
			continue
		}
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
		Output:     fmt.Sprintf(`{"indexed":%d,"skipped":%d}`, indexed, skipped),
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
	case "user_posts":
		return fetchUserPostsPDS(handle, limit)
	default:
		return nil, fmt.Errorf("unknown source %q; must be feed, notifications, profile, or user_posts", source)
	}
}

// fetchUserPostsPDS resolves a handle to its PDS via PLC directory, then
// fetches posts directly using com.atproto.repo.listRecords. This bypasses
// the appview entirely and works even when bsky.social is down.
func fetchUserPostsPDS(handle string, limit int) ([]indexRecord, error) {
	if handle == "" {
		return nil, fmt.Errorf("handle is required for source=user_posts")
	}

	// 1. Resolve handle → DID (try appview first, then PLC/handle well-known)
	did, err := resolveHandleToDID(handle)
	if err != nil {
		return nil, fmt.Errorf("resolve handle %q: %w", handle, err)
	}

	// 2. Look up PDS endpoint from PLC directory
	pdsURL, err := resolvePDSEndpoint(did)
	if err != nil {
		return nil, fmt.Errorf("resolve PDS for %s: %w", did, err)
	}

	// 3. Fetch posts via com.atproto.repo.listRecords directly from PDS
	params := url.Values{
		"repo":       {did},
		"collection": {"app.bsky.feed.post"},
		"limit":      {fmt.Sprintf("%d", limit)},
	}
	u := pdsURL + "/xrpc/com.atproto.repo.listRecords?" + params.Encode()

	resp, err := http.Get(u) //nolint:gosec // PDS URL is constructed from PLC directory
	if err != nil {
		return nil, fmt.Errorf("fetch posts from PDS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("PDS listRecords (%d): %s", resp.StatusCode, string(data))
	}

	var listResp struct {
		Records []struct {
			URI   string `json:"uri"`
			Value struct {
				Text      string `json:"text"`
				CreatedAt string `json:"createdAt"`
			} `json:"value"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &listResp); err != nil {
		return nil, fmt.Errorf("parse posts: %w", err)
	}

	out := make([]indexRecord, 0, len(listResp.Records))
	for _, r := range listResp.Records {
		text := strings.TrimSpace(r.Value.Text)
		if text == "" {
			continue
		}
		out = append(out, indexRecord{
			text:       fmt.Sprintf("@%s: %s", handle, text),
			uri:        r.URI,
			author:     handle,
			recordType: "post",
		})
	}
	return out, nil
}

// resolveHandleToDID resolves a Bluesky handle to its DID, trying multiple
// methods: appview XRPC, PLC directory, and handle well-known.
func resolveHandleToDID(handle string) (string, error) {
	hc := &http.Client{Timeout: 10 * time.Second}

	// Try appview resolveHandle
	u := "https://bsky.social/xrpc/com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(handle)
	resp, err := hc.Get(u)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			var out struct {
				DID string `json:"did"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err == nil && out.DID != "" {
				return out.DID, nil
			}
		}
	}

	// Try handle well-known
	resp2, err := hc.Get("https://" + handle + "/.well-known/atproto-did")
	if err == nil {
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp2.Body)
			did := strings.TrimSpace(string(body))
			if strings.HasPrefix(did, "did:") {
				return did, nil
			}
		}
	}

	// Try DNS TXT record via DNS-over-HTTPS
	resp3, err := hc.Get("https://dns.google/resolve?name=_atproto." + handle + "&type=TXT")
	if err == nil {
		defer func() { _ = resp3.Body.Close() }()
		if resp3.StatusCode == http.StatusOK {
			var dnsResp struct {
				Answer []struct {
					Data string `json:"data"`
				} `json:"Answer"`
			}
			if err := json.NewDecoder(resp3.Body).Decode(&dnsResp); err == nil {
				for _, a := range dnsResp.Answer {
					d := strings.Trim(a.Data, `"`)
					if strings.HasPrefix(d, "did=") {
						return strings.TrimPrefix(d, "did="), nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("could not resolve handle %q via any method", handle)
}

// resolvePDSEndpoint looks up a DID's PDS service endpoint from the PLC directory.
func resolvePDSEndpoint(did string) (string, error) {
	if !strings.HasPrefix(did, "did:plc:") {
		return "", fmt.Errorf("unsupported DID method: %s (only did:plc is supported)", did)
	}
	u := "https://plc.directory/" + did
	resp, err := http.Get(u) //nolint:gosec // PLC directory URL is well-known
	if err != nil {
		return "", fmt.Errorf("PLC directory lookup: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("PLC directory (%d): %s", resp.StatusCode, string(data))
	}

	var doc struct {
		Service []struct {
			ID              string `json:"id"`
			Type            string `json:"type"`
			ServiceEndpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse PLC doc: %w", err)
	}
	for _, s := range doc.Service {
		if s.Type == "AtprotoPersonalDataServer" {
			return strings.TrimRight(s.ServiceEndpoint, "/"), nil
		}
	}
	return "", fmt.Errorf("no AtprotoPersonalDataServer service found in DID document")
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
	return json.RawMessage(`{"type":"object","properties":{"results":{"type":"array"},"count":{"type":"integer"}}}`)
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

	// Build lightweight output without raw embedding vectors (saves ~4KB per result).
	type recallItem struct {
		Content    string  `json:"content"`
		RecordType string  `json:"record_type"`
		Author     string  `json:"author,omitempty"`
		URI        string  `json:"uri,omitempty"`
		Score      float32 `json:"score"`
	}
	var items []recallItem
	for _, r := range results {
		items = append(items, recallItem{
			Content:    r.Item.Content,
			RecordType: r.Item.Metadata.Tags["record_type"],
			Author:     r.Item.Metadata.Tags["author"],
			URI:        r.Item.Metadata.Tags["uri"],
			Score:      r.Score,
		})
	}

	b, err := json.Marshal(map[string]any{"results": items, "count": len(items)})
	if err != nil {
		return failResult(start, "marshal results: "+err.Error()), nil
	}
	return ToolResult{
		OK:         true,
		Output:     string(b),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}
