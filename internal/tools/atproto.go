package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

// ---------------------------------------------------------------------------
// atproto_feed — read the authenticated user's home timeline
// ---------------------------------------------------------------------------

type atprotoFeedTool struct{ cfg *config.Config }

// ATProtoFeed returns the atproto_feed tool.
func ATProtoFeed(cfg *config.Config) Tool { return &atprotoFeedTool{cfg: cfg} }

func (t *atprotoFeedTool) Name() string { return "atproto_feed" }
func (t *atprotoFeedTool) Description() string {
	return "Read the authenticated Bluesky user's home timeline. Returns a compact list of recent posts with author, text, engagement counts, and a cursor for pagination."
}
func (t *atprotoFeedTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoFeedTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoFeedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit":   {"type": "integer", "description": "Number of posts to fetch (1–100, default 20)."},
			"cursor":  {"type": "string",  "description": "Pagination cursor from a previous call."},
			"account": {"type": "string",  "description": "Which account to use: \"main\" (default) or \"alt\"."}
		}
	}`)
}

func (t *atprotoFeedTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"},
			"cursor": {"type": "string"}
		}
	}`)
}

func (t *atprotoFeedTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Limit   int    `json:"limit"`
		Cursor  string `json:"cursor"`
		Account string `json:"account"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	accountCfg, err := pickATProtoAccount(t.cfg, in.Account)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	cli := newATProtoClient(accountCfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	params := url.Values{"limit": {fmt.Sprintf("%d", in.Limit)}}
	if in.Cursor != "" {
		params.Set("cursor", in.Cursor)
	}
	data, err := cli.xrpcGet("app.bsky.feed.getTimeline", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
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
				ReplyCount  int `json:"replyCount"`
				RepostCount int `json:"repostCount"`
				LikeCount   int `json:"likeCount"`
			} `json:"post"`
		} `json:"feed"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return ToolResult{OK: false, Output: "parse error: " + err.Error()}, nil
	}

	var sb strings.Builder
	for _, item := range resp.Feed {
		p := item.Post
		name := p.Author.Handle
		if p.Author.DisplayName != "" {
			name = p.Author.DisplayName + " (@" + p.Author.Handle + ")"
		}
		fmt.Fprintf(&sb, "[%s] %s\n  %s\n  ♻ %d  ♥ %d  💬 %d\n\n",
			p.Record.CreatedAt, name, p.Record.Text,
			p.RepostCount, p.LikeCount, p.ReplyCount)
	}
	if resp.Cursor != "" {
		fmt.Fprintf(&sb, "cursor: %s", resp.Cursor)
	}
	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

// ---------------------------------------------------------------------------
// atproto_notifications — fetch mentions and activity notifications
// ---------------------------------------------------------------------------

type atprotoNotificationsTool struct{ cfg *config.Config }

// ATProtoNotifications returns the atproto_notifications tool.
func ATProtoNotifications(cfg *config.Config) Tool {
	return &atprotoNotificationsTool{cfg: cfg}
}

func (t *atprotoNotificationsTool) Name() string { return "atproto_notifications" }
func (t *atprotoNotificationsTool) Description() string {
	return "Fetch Bluesky notifications (mentions, replies, likes, reposts, follows). Supports filtering to unread-only."
}
func (t *atprotoNotificationsTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoNotificationsTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoNotificationsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"limit":       {"type": "integer", "description": "Max notifications to fetch (1–100, default 20)."},
			"unread_only": {"type": "boolean", "description": "Return only unread notifications (default false)."},
			"account":     {"type": "string",  "description": "Which account to use: \"main\" (default) or \"alt\"."}
		}
	}`)
}

func (t *atprotoNotificationsTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoNotificationsTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Limit      int    `json:"limit"`
		UnreadOnly bool   `json:"unread_only"`
		Account    string `json:"account"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	accountCfg, err := pickATProtoAccount(t.cfg, in.Account)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	cli := newATProtoClient(accountCfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	params := url.Values{"limit": {fmt.Sprintf("%d", in.Limit)}}
	data, err := cli.xrpcGet("app.bsky.notification.listNotifications", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
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
			IndexedAt string `json:"indexedAt"`
			IsRead    bool   `json:"isRead"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return ToolResult{OK: false, Output: "parse error: " + err.Error()}, nil
	}

	var sb strings.Builder
	count := 0
	for _, n := range resp.Notifications {
		if in.UnreadOnly && n.IsRead {
			continue
		}
		readTag := ""
		if !n.IsRead {
			readTag = " [unread]"
		}
		name := "@" + n.Author.Handle
		if n.Author.DisplayName != "" {
			name = n.Author.DisplayName + " (" + name + ")"
		}
		text := n.Record.Text
		if text != "" {
			text = "\n  " + text
		}
		fmt.Fprintf(&sb, "[%s]%s %s — %s%s\n\n",
			n.IndexedAt, readTag, n.Reason, name, text)
		count++
	}
	if count == 0 {
		return ToolResult{OK: true, Output: "no notifications"}, nil
	}
	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

// ---------------------------------------------------------------------------
// atproto_post — publish a post, repost, or quote post
// ---------------------------------------------------------------------------

type atprotoPostTool struct{ cfg *config.Config }

// ATProtoPost returns the atproto_post tool.
func ATProtoPost(cfg *config.Config) Tool { return &atprotoPostTool{cfg: cfg} }

func (t *atprotoPostTool) Name() string { return "atproto_post" }
func (t *atprotoPostTool) Description() string {
	return "Publish to Bluesky. Supports plain posts, replies (reply_to_uri + reply_to_cid), quote posts (quote_uri + quote_cid), reposts (repost_uri + repost_cid; text ignored), and image posts via the images array (each item: cid, mime, size, optional alt - use atproto_upload_blob first to obtain these). Quote+images are combined as a recordWithMedia embed."
}
func (t *atprotoPostTool) DangerLevel() DangerLevel { return Dangerous }
func (t *atprotoPostTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *atprotoPostTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"text":         {"type": "string",  "description": "Post text. Required for plain posts, replies, and quote posts. Not needed for reposts."},
			"reply_to_uri": {"type": "string",  "description": "AT URI of the immediate parent post being replied to."},
			"reply_to_cid": {"type": "string",  "description": "CID of the immediate parent post (required with reply_to_uri)."},
			"root_uri":     {"type": "string",  "description": "AT URI of the thread root post. Defaults to reply_to_uri when omitting (top-level reply). Must be set explicitly for nested replies."},
			"root_cid":     {"type": "string",  "description": "CID of the thread root post. Defaults to reply_to_cid when omitting (top-level reply). Must be set explicitly for nested replies."},
			"quote_uri":    {"type": "string",  "description": "AT URI of the post to quote."},
			"quote_cid":    {"type": "string",  "description": "CID of the post to quote (required with quote_uri)."},
			"repost_uri":   {"type": "string",  "description": "AT URI of the post to repost (text not required)."},
			"repost_cid":   {"type": "string",  "description": "CID of the post to repost (required with repost_uri)."},
			"images":       {"type": "array",   "description": "Optional image attachments. Each item: {cid, mime, size, alt?}. Obtain cid/mime/size by calling atproto_upload_blob first.", "items": {"type": "object", "properties": {"cid": {"type": "string"}, "mime": {"type": "string"}, "size": {"type": "integer"}, "alt": {"type": "string"}}, "required": ["cid", "mime", "size"]}},
			"account":      {"type": "string",  "description": "Which account to post from: \"main\" (default, charlebois.info) or \"alt\" (xx-c.art)."}
		}
	}`)
}

func (t *atprotoPostTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":  {"type": "boolean"},
			"uri": {"type": "string"},
			"cid": {"type": "string"}
		}
	}`)
}

func (t *atprotoPostTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Text       string `json:"text"`
		ReplyToURI string `json:"reply_to_uri"`
		ReplyToCID string `json:"reply_to_cid"`
		RootURI    string `json:"root_uri"`
		RootCID    string `json:"root_cid"`
		QuoteURI   string `json:"quote_uri"`
		QuoteCID   string `json:"quote_cid"`
		RepostURI  string `json:"repost_uri"`
		RepostCID  string `json:"repost_cid"`
		Images     []struct {
			CID  string `json:"cid"`
			Mime string `json:"mime"`
			Size int64  `json:"size"`
			Alt  string `json:"alt"`
		} `json:"images"`
		Account string `json:"account"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}

	accountCfg, err := pickATProtoAccount(t.cfg, in.Account)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	cli := newATProtoClient(accountCfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	// Repost path — separate lexicon.
	if in.RepostURI != "" && in.RepostCID != "" {
		payload := map[string]any{
			"repo":       cli.session.DID,
			"collection": "app.bsky.feed.repost",
			"record": map[string]any{
				"$type":     "app.bsky.feed.repost",
				"subject":   map[string]string{"uri": in.RepostURI, "cid": in.RepostCID},
				"createdAt": time.Now().UTC().Format(time.RFC3339),
			},
		}
		data, err := cli.xrpcPost("com.atproto.repo.createRecord", payload)
		if err != nil {
			return ToolResult{OK: false, Output: err.Error()}, nil
		}
		var out struct {
			URI string `json:"uri"`
			CID string `json:"cid"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return ToolResult{OK: true, Output: string(data)}, nil
		}
		return ToolResult{OK: true, Output: fmt.Sprintf("uri=%s cid=%s", out.URI, out.CID)}, nil
	}

	if in.Text == "" && len(in.Images) == 0 {
		return ToolResult{OK: false, Output: "text is required"}, nil
	}

	// Build post record.
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      in.Text,
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	}

	// Reply block. root defaults to parent for top-level replies; must be set
	// explicitly for nested replies to preserve correct thread structure.
	if in.ReplyToURI != "" && in.ReplyToCID != "" {
		rootURI := in.RootURI
		rootCID := in.RootCID
		if rootURI == "" {
			rootURI = in.ReplyToURI
		}
		if rootCID == "" {
			rootCID = in.ReplyToCID
		}
		record["reply"] = map[string]any{
			"root":   map[string]string{"uri": rootURI, "cid": rootCID},
			"parent": map[string]string{"uri": in.ReplyToURI, "cid": in.ReplyToCID},
		}
	}

	// Build embeds. Images and quotes can coexist via recordWithMedia.
	var imagesEmbed map[string]any
	if len(in.Images) > 0 {
		if len(in.Images) > 4 {
			return ToolResult{OK: false, Output: "atproto: at most 4 images per post"}, nil
		}
		items := make([]map[string]any, len(in.Images))
		for i, img := range in.Images {
			if img.CID == "" || img.Mime == "" || img.Size <= 0 {
				return ToolResult{OK: false, Output: fmt.Sprintf("images[%d]: cid, mime, and size are required", i)}, nil
			}
			items[i] = map[string]any{
				"alt": img.Alt,
				"image": map[string]any{
					"$type":    "blob",
					"ref":      map[string]string{"$link": img.CID},
					"mimeType": img.Mime,
					"size":     img.Size,
				},
			}
		}
		imagesEmbed = map[string]any{
			"$type":  "app.bsky.embed.images",
			"images": items,
		}
	}

	var quoteEmbed map[string]any
	if in.QuoteURI != "" && in.QuoteCID != "" {
		quoteEmbed = map[string]any{
			"$type":  "app.bsky.embed.record",
			"record": map[string]string{"uri": in.QuoteURI, "cid": in.QuoteCID},
		}
	}

	switch {
	case imagesEmbed != nil && quoteEmbed != nil:
		record["embed"] = map[string]any{
			"$type":  "app.bsky.embed.recordWithMedia",
			"record": quoteEmbed,
			"media":  imagesEmbed,
		}
	case imagesEmbed != nil:
		record["embed"] = imagesEmbed
	case quoteEmbed != nil:
		record["embed"] = quoteEmbed
	}

	payload := map[string]any{
		"repo":       cli.session.DID,
		"collection": "app.bsky.feed.post",
		"record":     record,
	}
	data, err := cli.xrpcPost("com.atproto.repo.createRecord", payload)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	var out struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return ToolResult{OK: true, Output: string(data)}, nil
	}
	return ToolResult{OK: true, Output: fmt.Sprintf("uri=%s cid=%s", out.URI, out.CID)}, nil
}

// ---------------------------------------------------------------------------
// atproto_resolve — resolve a handle to DID
// ---------------------------------------------------------------------------

type atprotoResolveTool struct{ cfg config.ATProtoConfig }

// ATProtoResolve returns the atproto_resolve tool.
func ATProtoResolve(cfg *config.Config) Tool { return &atprotoResolveTool{cfg: cfg.ATProto} }

func (t *atprotoResolveTool) Name() string { return "atproto_resolve" }
func (t *atprotoResolveTool) Description() string {
	return "Resolve a Bluesky handle to its DID. Useful before constructing repost or quote post payloads that require a DID."
}
func (t *atprotoResolveTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoResolveTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoResolveTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["handle"],
		"properties": {
			"handle": {"type": "string", "description": "Bluesky handle, e.g. 'alice.bsky.social'."}
		}
	}`)
}

func (t *atprotoResolveTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"did":    {"type": "string"},
			"handle": {"type": "string"}
		}
	}`)
}

func (t *atprotoResolveTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Handle == "" {
		return ToolResult{OK: false, Output: "handle is required"}, nil
	}

	// com.atproto.identity.resolveHandle is a public unauthenticated endpoint;
	// no login required.
	cli := newATProtoClient(t.cfg)
	params := url.Values{"handle": {in.Handle}}
	data, err := cli.xrpcGet("com.atproto.identity.resolveHandle", params)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	var out struct {
		DID string `json:"did"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return ToolResult{OK: false, Output: "parse error: " + err.Error()}, nil
	}
	return ToolResult{OK: true, Output: fmt.Sprintf("did=%s handle=%s", out.DID, in.Handle)}, nil
}

// ---------------------------------------------------------------------------
// atproto_upload_blob — upload an image blob to Bluesky
// ---------------------------------------------------------------------------

type atprotoUploadBlobTool struct{ cfg *config.Config }

// ATProtoUploadBlob returns the atproto_upload_blob tool.
func ATProtoUploadBlob(cfg *config.Config) Tool { return &atprotoUploadBlobTool{cfg: cfg} }

func (t *atprotoUploadBlobTool) Name() string { return "atproto_upload_blob" }
func (t *atprotoUploadBlobTool) Description() string {
	return "Upload an image blob to Bluesky and return its CID for use in posts. " +
		"Call this first, then pass the returned cid, mime, size, and optional alt to atproto_post via the images array."
}
func (t *atprotoUploadBlobTool) DangerLevel() DangerLevel { return Dangerous }
func (t *atprotoUploadBlobTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *atprotoUploadBlobTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["image_path"],
		"properties": {
			"image_path": {"type": "string", "description": "Path to the image file to upload (PNG, JPEG, GIF, WebP)."},
			"alt_text":   {"type": "string", "description": "Optional alt text. Echoed back in the result so it can be passed straight into atproto_post images[]."},
			"account":    {"type": "string", "description": "Which account to use: \"main\" (default) or \"alt\"."}
		}
	}`)
}

func (t *atprotoUploadBlobTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":   {"type": "boolean"},
			"cid":  {"type": "string"},
			"mime": {"type": "string"},
			"size": {"type": "integer"},
			"alt":  {"type": "string"}
		}
	}`)
}

func (t *atprotoUploadBlobTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		ImagePath string `json:"image_path"`
		AltText   string `json:"alt_text"`
		Account   string `json:"account"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.ImagePath == "" {
		return ToolResult{OK: false, Output: "image_path is required"}, nil
	}

	accountCfg, err := pickATProtoAccount(t.cfg, in.Account)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	cli := newATProtoClient(accountCfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	data, err := os.ReadFile(in.ImagePath)
	if err != nil {
		return ToolResult{OK: false, Output: "failed to read image file: " + err.Error()}, nil
	}

	// Detect MIME from file contents (magic bytes); fall back / cross-check
	// against extension. http.DetectContentType reads up to the first 512 bytes.
	detected := http.DetectContentType(data)
	// Strip charset suffix if any, e.g. "image/svg+xml; charset=utf-8".
	if i := strings.IndexByte(detected, ';'); i >= 0 {
		detected = strings.TrimSpace(detected[:i])
	}
	mime := detected
	if !isSupportedBskyImage(mime) {
		// DetectContentType returns "application/octet-stream" for unknowns.
		// Try the extension as a last-ditch hint, but only accept if supported.
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(in.ImagePath), "."))
		if alt := mimeByExt(ext); isSupportedBskyImage(alt) {
			mime = alt
		} else {
			return ToolResult{OK: false, Output: fmt.Sprintf("unsupported image type: detected=%q ext=%q (want png/jpeg/gif/webp)", detected, ext)}, nil
		}
	}

	blob, err := cli.xrpcUploadBlob(filepath.Base(in.ImagePath), mime, data)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	out, _ := json.Marshal(map[string]any{
		"cid":  blob.CID,
		"mime": blob.Mime,
		"size": blob.Size,
		"alt":  in.AltText,
	})
	return ToolResult{OK: true, Output: string(out)}, nil
}

func isSupportedBskyImage(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// mimeByExt maps file extensions to MIME types (fallback when magic-byte
// detection is inconclusive).
func mimeByExt(ext string) string {
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}
