package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tripledoublev/v100/internal/config"
)

// ---------------------------------------------------------------------------
// digestPost — internal representation of a post for digest operations
// ---------------------------------------------------------------------------

type digestPost struct {
	URI          string
	Author       string
	AuthorHandle string
	Text         string
	CreatedAt    time.Time
	Likes        int
	Reposts      int
	Replies      int
}

// engagementScore computes: likes + (reposts * 2) + replies
func engagementScore(p digestPost) int {
	return p.Likes + (p.Reposts * 2) + p.Replies
}

// topWords extracts the top N most frequent non-stopword tokens.
func topWords(posts []digestPost, n int) []string {
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
		"i": true, "is": true, "it": true, "in": true, "to": true, "for": true,
		"of": true, "with": true, "on": true, "at": true, "my": true, "me": true,
		"we": true, "you": true, "be": true, "this": true, "that": true, "are": true,
		"was": true, "have": true, "not": true, "so": true, "just": true, "no": true,
		"do": true, "from": true, "by": true, "as": true, "if": true, "has": true,
	}
	freq := make(map[string]int)
	for _, p := range posts {
		tokens := strings.FieldsFunc(p.Text, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
				(r < '0' || r > '9') && r != '_' && r != '#' && r != '@'
		})
		for _, tok := range tokens {
			tok = strings.ToLower(tok)
			if len(tok) > 2 && !stopwords[tok] {
				freq[tok]++
			}
		}
	}
	type kv struct {
		word  string
		count int
	}
	var sorted []kv
	for w, c := range freq {
		sorted = append(sorted, kv{w, c})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	var result []string
	for i := 0; i < len(sorted) && i < n; i++ {
		result = append(result, sorted[i].word)
	}
	return result
}

// fetchFilteredFeed fetches posts from the feed and filters by hours.
func fetchFilteredFeed(ctx context.Context, cli *atProtoClient, hours int, limit int) ([]digestPost, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	var result []digestPost
	seenURIs := make(map[string]bool)
	var cursor string

	for len(result) < limit {
		params := url.Values{"limit": {fmt.Sprintf("%d", minInt(20, limit-len(result)))}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
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
					LikeCount   int `json:"likeCount"`
					RepostCount int `json:"repostCount"`
					ReplyCount  int `json:"replyCount"`
				} `json:"post"`
			} `json:"feed"`
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}

		for _, item := range resp.Feed {
			createdAt, err := time.Parse(time.RFC3339, item.Post.Record.CreatedAt)
			if err != nil {
				continue // skip posts with unparseable timestamps
			}
			if createdAt.Before(cutoff) {
				continue // skip posts outside window
			}
			if seenURIs[item.Post.URI] {
				continue // skip duplicate posts
			}
			seenURIs[item.Post.URI] = true
			result = append(result, digestPost{
				URI:          item.Post.URI,
				Author:       item.Post.Author.DisplayName,
				AuthorHandle: item.Post.Author.Handle,
				Text:         item.Post.Record.Text,
				CreatedAt:    createdAt,
				Likes:        item.Post.LikeCount,
				Reposts:      item.Post.RepostCount,
				Replies:      item.Post.ReplyCount,
			})
			if len(result) >= limit {
				break
			}
		}

		cursor = resp.Cursor
		if cursor == "" {
			break
		}
	}
	return result, nil
}

func fetchAuthorFeed(cli *atProtoClient, actor string, limit int) ([]digestPost, error) {
	if limit <= 0 {
		limit = 50
	}
	params := url.Values{
		"actor": {actor},
		"limit": {fmt.Sprintf("%d", limit)},
	}
	data, err := cli.xrpcGet("app.bsky.feed.getAuthorFeed", params)
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
				LikeCount   int `json:"likeCount"`
				RepostCount int `json:"repostCount"`
				ReplyCount  int `json:"replyCount"`
			} `json:"post"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	posts := make([]digestPost, 0, len(resp.Feed))
	for _, item := range resp.Feed {
		createdAt, err := time.Parse(time.RFC3339, item.Post.Record.CreatedAt)
		if err != nil {
			continue
		}
		posts = append(posts, digestPost{
			URI:          item.Post.URI,
			Author:       item.Post.Author.DisplayName,
			AuthorHandle: item.Post.Author.Handle,
			Text:         item.Post.Record.Text,
			CreatedAt:    createdAt,
			Likes:        item.Post.LikeCount,
			Reposts:      item.Post.RepostCount,
			Replies:      item.Post.ReplyCount,
		})
	}
	return posts, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// atproto_engagement_health tool
// ---------------------------------------------------------------------------

type atprotoEngagementHealthTool struct{ cfg config.ATProtoConfig }

// ATProtoEngagementHealth returns the engagement_health tool.
func ATProtoEngagementHealth(cfg *config.Config) Tool {
	return &atprotoEngagementHealthTool{cfg: cfg.ATProto}
}

func (t *atprotoEngagementHealthTool) Name() string { return "atproto_engagement_health" }
func (t *atprotoEngagementHealthTool) Description() string {
	return "Track your Bluesky posting patterns and engagement health. Summarizes cadence, average engagement, best posts, recurring topics, and practical next actions."
}
func (t *atprotoEngagementHealthTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoEngagementHealthTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoEngagementHealthTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"actor": {"type": "string", "description": "Handle or DID to analyze. Defaults to the authenticated user."},
			"limit": {"type": "integer", "description": "Recent posts to inspect (default 50, max 100)."}
		}
	}`)
}

func (t *atprotoEngagementHealthTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoEngagementHealthTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Actor string `json:"actor"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = cli.session.DID
	}

	posts, err := fetchAuthorFeed(cli, actor, in.Limit)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}
	if len(posts) == 0 {
		return ToolResult{OK: true, Output: "no recent posts found to analyze."}, nil
	}

	sort.Slice(posts, func(i, j int) bool {
		return posts[i].CreatedAt.After(posts[j].CreatedAt)
	})
	totalEngagement := 0
	replyPosts := 0
	quoteOrLinkish := 0
	for _, post := range posts {
		totalEngagement += engagementScore(post)
		if strings.HasPrefix(strings.TrimSpace(post.Text), "@") {
			replyPosts++
		}
		if strings.Contains(post.Text, "http://") || strings.Contains(post.Text, "https://") || strings.Contains(post.Text, "quote") {
			quoteOrLinkish++
		}
	}
	avgEngagement := float64(totalEngagement) / float64(len(posts))
	newest := posts[0].CreatedAt
	oldest := posts[len(posts)-1].CreatedAt
	windowHours := newest.Sub(oldest).Hours()
	if windowHours < 1 {
		windowHours = 1
	}
	postsPerDay := float64(len(posts)) / (windowHours / 24)

	top := append([]digestPost(nil), posts...)
	sort.Slice(top, func(i, j int) bool {
		return engagementScore(top[i]) > engagementScore(top[j])
	})
	if len(top) > 3 {
		top = top[:3]
	}

	topWordsList := topWords(posts, 8)
	suggestions := engagementHealthSuggestions(postsPerDay, avgEngagement, replyPosts, quoteOrLinkish, len(posts))

	var sb strings.Builder
	fmt.Fprintf(&sb, "Engagement health for %s\n", actor)
	fmt.Fprintf(&sb, "posts analyzed: %d · cadence: %.1f/day · avg engagement: %.1f · replies: %d%%\n", len(posts), postsPerDay, avgEngagement, replyPosts*100/len(posts))
	if len(topWordsList) > 0 {
		fmt.Fprintf(&sb, "recurring topics: %s\n", strings.Join(topWordsList, ", "))
	}
	fmt.Fprintf(&sb, "\nTop posts:\n")
	for idx, post := range top {
		fmt.Fprintf(&sb, "%d. [%d] %s\n", idx+1, engagementScore(post), post.Text)
	}
	fmt.Fprintf(&sb, "\nSuggested actions:\n")
	for _, suggestion := range suggestions {
		fmt.Fprintf(&sb, "- %s\n", suggestion)
	}
	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

func engagementHealthSuggestions(postsPerDay, avgEngagement float64, replyPosts, quoteOrLinkish, totalPosts int) []string {
	var out []string
	switch {
	case postsPerDay < 0.5:
		out = append(out, "Increase cadence: recent posting is sparse, so test a predictable daily post window.")
	case postsPerDay > 6:
		out = append(out, "Reduce burstiness: many posts per day can fragment attention; consolidate weaker updates.")
	default:
		out = append(out, "Cadence looks sustainable; keep testing repeatable formats around your strongest topics.")
	}
	if avgEngagement < 2 {
		out = append(out, "Ask more direct questions or add clearer context hooks to invite replies.")
	} else {
		out = append(out, "Double down on posts similar to the top performers by topic and format.")
	}
	if totalPosts > 0 && replyPosts*100/totalPosts < 20 {
		out = append(out, "Add more conversational replies; your recent mix is mostly standalone posts.")
	}
	if totalPosts > 0 && quoteOrLinkish*100/totalPosts > 60 {
		out = append(out, "Balance link-heavy posts with original summaries or takeaways.")
	}
	return out
}

// ---------------------------------------------------------------------------
// atproto_vibe_check tool
// ---------------------------------------------------------------------------

type atprotoVibeCheckTool struct{ cfg config.ATProtoConfig }

// ATProtoVibeCheck returns the vibe_check tool.
func ATProtoVibeCheck(cfg *config.Config) Tool {
	return &atprotoVibeCheckTool{cfg: cfg.ATProto}
}

func (t *atprotoVibeCheckTool) Name() string { return "atproto_vibe_check" }
func (t *atprotoVibeCheckTool) Description() string {
	return "Analyze your Bluesky network's sentiment and mood. Fetches recent posts and surfaces the vibe: what topics people are discussing and how they feel about them."
}
func (t *atprotoVibeCheckTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoVibeCheckTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoVibeCheckTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"hours": {"type": "integer", "description": "Lookback window in hours (default 12, max 48)."},
			"limit": {"type": "integer", "description": "Max posts to fetch (default 40, max 100)."}
		}
	}`)
}

func (t *atprotoVibeCheckTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoVibeCheckTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Hours int `json:"hours"`
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}

	if in.Hours <= 0 {
		in.Hours = 12
	}
	if in.Hours > 48 {
		in.Hours = 48
	}
	if in.Limit <= 0 {
		in.Limit = 40
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	posts, err := fetchFilteredFeed(context.Background(), cli, in.Hours, in.Limit)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	if len(posts) == 0 {
		return ToolResult{OK: true, Output: "no posts in the last " + fmt.Sprintf("%d", in.Hours) + "h"}, nil
	}

	// Collect engagement stats and top words
	topWordsList := topWords(posts, 10)
	topWordsStr := strings.Join(topWordsList, ", ")

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d posts from the last %dh · top words: %s\n\n",
		len(posts), in.Hours, topWordsStr)

	for _, p := range posts {
		fmt.Fprintf(&sb, "@%s [👍%d 🔁%d 💬%d] %s\n\n",
			p.AuthorHandle, p.Likes, p.Reposts, p.Replies, p.Text)
	}

	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}

// ---------------------------------------------------------------------------
// atproto_daily_digest tool
// ---------------------------------------------------------------------------

type atprotoDailyDigestTool struct{ cfg config.ATProtoConfig }

// ATProtoDailyDigest returns the daily_digest tool.
func ATProtoDailyDigest(cfg *config.Config) Tool {
	return &atprotoDailyDigestTool{cfg: cfg.ATProto}
}

func (t *atprotoDailyDigestTool) Name() string { return "atproto_daily_digest" }
func (t *atprotoDailyDigestTool) Description() string {
	return "Generate a daily briefing of your Bluesky feed. Fetches recent posts, ranks by engagement, and produces a curated list of what's trending in your network."
}
func (t *atprotoDailyDigestTool) DangerLevel() DangerLevel { return Safe }
func (t *atprotoDailyDigestTool) Effects() ToolEffects     { return ToolEffects{NeedsNetwork: true} }

func (t *atprotoDailyDigestTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"hours":  {"type": "integer", "description": "Lookback window in hours (default 24, max 72)."},
			"limit":  {"type": "integer", "description": "Max posts to fetch (default 100, max 200)."},
			"top_n":  {"type": "integer", "description": "Top N posts to highlight (default 10)."}
		}
	}`)
}

func (t *atprotoDailyDigestTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"ok":     {"type": "boolean"},
			"output": {"type": "string"}
		}
	}`)
}

func (t *atprotoDailyDigestTool) Exec(_ context.Context, _ ToolCallContext, args json.RawMessage) (ToolResult, error) {
	var in struct {
		Hours int `json:"hours"`
		Limit int `json:"limit"`
		TopN  int `json:"top_n"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return ToolResult{OK: false, Output: "invalid args: " + err.Error()}, nil
	}

	if in.Hours <= 0 {
		in.Hours = 24
	}
	if in.Hours > 72 {
		in.Hours = 72
	}
	if in.Limit <= 0 {
		in.Limit = 100
	}
	if in.Limit > 200 {
		in.Limit = 200
	}
	if in.TopN <= 0 {
		in.TopN = 10
	}

	cli := newATProtoClient(t.cfg)
	if err := cli.login(); err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	posts, err := fetchFilteredFeed(context.Background(), cli, in.Hours, in.Limit)
	if err != nil {
		return ToolResult{OK: false, Output: err.Error()}, nil
	}

	if len(posts) == 0 {
		return ToolResult{OK: true, Output: "no posts in the last " + fmt.Sprintf("%d", in.Hours) + "h"}, nil
	}

	// Sort by engagement score (descending)
	sort.Slice(posts, func(i, j int) bool {
		return engagementScore(posts[i]) > engagementScore(posts[j])
	})

	// Take top N
	topPosts := posts
	if len(topPosts) > in.TopN {
		topPosts = topPosts[:in.TopN]
	}

	topWordsList := topWords(posts, 10)
	topWordsStr := strings.Join(topWordsList, ", ")

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d posts from the last %dh (showing top %d by engagement) · top words: %s\n\n",
		len(posts), in.Hours, len(topPosts), topWordsStr)

	for idx, p := range topPosts {
		score := engagementScore(p)
		fmt.Fprintf(&sb, "%d. @%s [🔥%d] %s\n",
			idx+1, p.AuthorHandle, score, p.Text)
	}

	return ToolResult{OK: true, Output: strings.TrimRight(sb.String(), "\n")}, nil
}
